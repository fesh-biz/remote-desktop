package biz.fesh.remotedesktop;

import android.content.Context;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.RectF;
import android.os.SystemClock;
import android.text.InputType;
import android.view.HapticFeedbackConstants;
import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewConfiguration;
import android.view.inputmethod.BaseInputConnection;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputConnection;

import org.json.JSONObject;

import java.util.ArrayDeque;

/**
 * Shows the remote screen and turns touches into mouse input.
 *
 * Touchpad mode: drag = move pointer, tap = left click, double tap = double click,
 * double tap and drag = drag with left button, long press / two-finger tap = right click,
 * two-finger drag = scroll, pinch = zoom.
 * Direct mode: the pointer goes where you touch; drag = drag with left button.
 *
 * With a protocol-2 server the pointer is not part of the picture: it is drawn here at
 * the display rate. Moves are applied locally at once and reconciled with the
 * server's reports (which say up to which move they include). When zoomed in, the view
 * keeps the pointer in the centre until it reaches an edge of the PC screen.
 */
final class RemoteView extends View {

    interface InputSink {
        void send(JSONObject msg);

        void sendText(String text);

        void tapKey(int vk);

        void keyEvent(int vk, boolean down);

        void toggleKeyboard();
    }

    private static final int DOUBLE_TAP_MS = 250;
    private static final int LONG_PRESS_MS = 500;
    private static final int TWO_UNDECIDED = 0, TWO_SCROLL = 1, TWO_ZOOM = 2;

    private InputSink sink;
    private Connection conn;

    private Bitmap frame;
    private float cursorX = -1, cursorY = -1; // pointer drawn into the frame (protocol 1), 0..1
    private int pendingAcks;
    private final Paint paint = new Paint(Paint.FILTER_BITMAP_FLAG | Paint.ANTI_ALIAS_FLAG);
    private final RectF dst = new RectF();

    // pointer drawn by the app (protocol 2), in physical pixels of the streamed monitor
    private boolean cursorChannel;
    private boolean haveCursor, curVisible;
    private int srvX, srvY, monW = 1, monH = 1;
    private int boundL, boundT, boundR = -1, boundB = -1; // where the pointer can go (all monitors)
    private float predX, predY;
    private int moveSeq;
    // {seq, 0, dx, dy} relative move in PC pixels, or {seq, 1, x, y} absolute in 1/65536 of the monitor
    private final ArrayDeque<int[]> pendingMoves = new ArrayDeque<>();
    private Bitmap shape;
    private int hotX, hotY;
    private final Path arrow = new Path();
    private final Paint arrowFill = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Paint arrowStroke = new Paint(Paint.ANTI_ALIAS_FLAG);

    private boolean touchpad = true;
    private float sensitivity = 1f;

    // view transform
    private float zoom = 1f;
    private float panX, panY;
    private boolean layoutValid;

    // gesture state
    private final float density;
    private final int touchSlop;
    private long downTime;
    private float x0, y0, lastX, lastY;
    private long lastMoveTime;
    private boolean moved, longFired, dragging, tapDrag;
    private int maxPointers;
    private float fracX, fracY;
    private long lastTapUp;
    private float lastTapX, lastTapY;
    private Runnable pendingClick;

    private int twoMode;
    private float span0, focus0X, focus0Y, lastFocusX, lastFocusY, zoom0, anchorX, anchorY;
    private float scrollAccX, scrollAccY;

    private final Runnable longPress = new Runnable() {
        @Override
        public void run() {
            if (moved || maxPointers != 1) return;
            longFired = true;
            performHapticFeedback(HapticFeedbackConstants.LONG_PRESS);
            if (!touchpad) moveAbs(x0, y0);
            click(1, 1);
        }
    };

    RemoteView(Context ctx) {
        super(ctx);
        density = ctx.getResources().getDisplayMetrics().density;
        touchSlop = ViewConfiguration.get(ctx).getScaledTouchSlop();
        setFocusable(true);
        setFocusableInTouchMode(true);
        setBackgroundColor(Color.BLACK);
        // fallback pointer until the PC sends its real image (32 px design units)
        arrow.moveTo(0, 0);
        arrow.lineTo(0, 22);
        arrow.lineTo(6, 17);
        arrow.lineTo(10, 26);
        arrow.lineTo(14, 24);
        arrow.lineTo(10, 16);
        arrow.lineTo(17, 16);
        arrow.close();
        arrowFill.setColor(Color.WHITE);
        arrowStroke.setColor(Color.BLACK);
        arrowStroke.setStyle(Paint.Style.STROKE);
        arrowStroke.setStrokeWidth(1.5f);
        arrowStroke.setStrokeJoin(Paint.Join.ROUND);
    }

    void setSink(InputSink sink, Connection conn) {
        this.sink = sink;
        this.conn = conn;
        pendingAcks = 0;
        cursorChannel = false;
        haveCursor = false;
        pendingMoves.clear();
    }

    /** The server sends the pointer separately (protocol 2). */
    void setCursorChannel(boolean on) {
        cursorChannel = on;
        haveCursor = false;
        pendingMoves.clear();
        invalidate();
    }

    /** Area the pointer can reach (the whole virtual screen), relative to the streamed monitor. */
    void setPointerBounds(int l, int t, int r, int b) {
        boundL = l;
        boundT = t;
        boundR = r;
        boundB = b;
    }

    /** Pointer report from the PC; it includes every move up to {@code seq}. */
    void setCursor(int x, int y, int w, int h, int seq, boolean visible) {
        srvX = x;
        srvY = y;
        monW = Math.max(1, w);
        monH = Math.max(1, h);
        curVisible = visible;
        haveCursor = true;
        while (!pendingMoves.isEmpty() && seqDone(pendingMoves.peekFirst()[0], seq)) pendingMoves.pollFirst();
        predict();
        followCursor();
        invalidate();
    }

    void setCursorShape(Bitmap bmp, int hx, int hy) {
        shape = bmp;
        hotX = hx;
        hotY = hy;
        invalidate();
    }

    private static boolean seqDone(int moveSeq, int ackSeq) {
        return moveSeq - ackSeq <= 0; // wrap-safe
    }

    private float clampX(float x) {
        int l = boundR >= boundL ? boundL : 0, r = boundR >= boundL ? boundR : monW - 1;
        return Math.max(l, Math.min(r, x));
    }

    private float clampY(float y) {
        int t = boundB >= boundT ? boundT : 0, b = boundB >= boundT ? boundB : monH - 1;
        return Math.max(t, Math.min(b, y));
    }

    /** Server position plus the moves it has not applied yet. */
    private void predict() {
        float x = srvX, y = srvY;
        for (int[] m : pendingMoves) {
            if (m[1] == 1) {
                x = Math.round(m[2] / 65536f * (monW - 1));
                y = Math.round(m[3] / 65536f * (monH - 1));
            } else {
                x = clampX(x + m[2]);
                y = clampY(y + m[3]);
            }
        }
        predX = x;
        predY = y;
    }

    private int nextSeq() {
        moveSeq++;
        if (moveSeq == 0) moveSeq = 1;
        return moveSeq;
    }

    private void trackMove(int seq, boolean abs, int a, int b) {
        if (!cursorChannel) return;
        if (pendingMoves.size() > 400) pendingMoves.pollFirst();
        pendingMoves.addLast(new int[]{seq, abs ? 1 : 0, a, b});
        if (haveCursor) {
            predict();
            followCursor();
            invalidate();
        }
    }

    /** Pointer position as a fraction of the monitor, or false if it is not on it / unknown. */
    private boolean pointerFraction(float[] out) {
        if (cursorChannel) {
            if (!haveCursor) return false;
            if (predX < 0 || predY < 0 || predX >= monW || predY >= monH) return false;
            out[0] = predX / monW;
            out[1] = predY / monH;
            return true;
        }
        if (cursorX < 0) return false;
        out[0] = cursorX;
        out[1] = cursorY;
        return true;
    }

    private final float[] tmp2 = new float[2];

    void setTouchpad(boolean touchpad) {
        this.touchpad = touchpad;
    }

    boolean isTouchpad() {
        return touchpad;
    }

    void setSensitivity(float s) {
        sensitivity = s;
    }

    boolean hasFrame() {
        return frame != null;
    }

    /** Called on the UI thread with a freshly decoded frame. */
    void setFrame(Bitmap bmp, float cx, float cy) {
        Bitmap old = frame;
        boolean sizeChanged = old == null || old.getWidth() != bmp.getWidth() || old.getHeight() != bmp.getHeight();
        frame = bmp;
        cursorX = cx;
        cursorY = cy;
        pendingAcks++;
        if (old != null && old != bmp && conn != null) conn.recycle(old);
        if (sizeChanged) layoutValid = false;
        followCursor();
        invalidate();
    }

    void resetZoom() {
        zoom = 1f;
        layoutValid = false;
        invalidate();
    }

    /** Toolbar zoom: 1× → 2× → 3× → 1×. In touchpad mode the pointer ends up in the centre. */
    float cycleZoom() {
        if (frame == null) return zoom;
        float nz = zoom < 1.5f ? 2f : zoom < 2.5f ? 3f : 1f;
        float s0 = scale();
        float ax = (getWidth() / 2f - panX) / s0, ay = (getHeight() / 2f - panY) / s0;
        zoom = nz;
        float s1 = scale();
        panX = getWidth() / 2f - ax * s1;
        panY = getHeight() / 2f - ay * s1;
        clampPan();
        layoutValid = true;
        followCursor();
        invalidate();
        return zoom;
    }

    // ------------------------------------------------------------ geometry

    private float baseScale() {
        if (frame == null || getWidth() == 0 || getHeight() == 0) return 1f;
        return Math.min(getWidth() / (float) frame.getWidth(), getHeight() / (float) frame.getHeight());
    }

    private float scale() {
        return baseScale() * zoom;
    }

    private float dispW() {
        return frame == null ? 0 : frame.getWidth() * scale();
    }

    private float dispH() {
        return frame == null ? 0 : frame.getHeight() * scale();
    }

    private void clampPan() {
        float w = dispW(), h = dispH();
        if (w <= getWidth()) panX = (getWidth() - w) / 2f;
        else panX = Math.max(getWidth() - w, Math.min(0, panX));
        if (h <= getHeight()) panY = (getHeight() - h) / 2f;
        else panY = Math.max(getHeight() - h, Math.min(0, panY));
    }

    @Override
    protected void onSizeChanged(int w, int h, int oldw, int oldh) {
        super.onSizeChanged(w, h, oldw, oldh);
        layoutValid = false;
    }

    /**
     * Touchpad mode, zoomed in: keep the pointer in the middle of the phone screen. Near an
     * edge of the PC screen the view stops (clampPan) and the pointer moves on the phone instead.
     */
    private void followCursor() {
        if (!touchpad || zoom <= 1.01f || frame == null || !pointerFraction(tmp2)) return;
        panX = getWidth() / 2f - tmp2[0] * dispW();
        panY = getHeight() / 2f - tmp2[1] * dispH();
        clampPan();
        layoutValid = true;
    }

    @Override
    protected void onDraw(Canvas canvas) {
        if (frame != null && !frame.isRecycled()) {
            if (!layoutValid) {
                clampPan();
                layoutValid = true;
            }
            dst.set(panX, panY, panX + dispW(), panY + dispH());
            canvas.drawBitmap(frame, null, dst, paint);
            if (cursorChannel && curVisible) drawPointer(canvas);
        }
        if (pendingAcks > 0 && conn != null) {
            for (int i = 0; i < pendingAcks; i++) conn.sendAck();
            pendingAcks = 0;
        }
    }

    private void drawPointer(Canvas canvas) {
        if (!pointerFraction(tmp2)) return;
        float px = panX + tmp2[0] * dispW();
        float py = panY + tmp2[1] * dispH();
        float k = dispW() / monW; // phone px per PC px
        if (shape != null) {
            float minH = 22 * density;
            if (shape.getHeight() * k < minH) k = minH / shape.getHeight();
            dst.set(px - hotX * k, py - hotY * k, px + (shape.getWidth() - hotX) * k, py + (shape.getHeight() - hotY) * k);
            canvas.drawBitmap(shape, null, dst, paint);
        } else {
            float a = Math.max(k, 22 * density / 26f);
            canvas.save();
            canvas.translate(px, py);
            canvas.scale(a, a);
            canvas.drawPath(arrow, arrowFill);
            canvas.drawPath(arrow, arrowStroke);
            canvas.restore();
        }
    }

    // ------------------------------------------------------------ sending

    private void send(JSONObject o) {
        if (sink != null) sink.send(o);
    }

    private void moveAbs(float vx, float vy) {
        if (frame == null) return;
        float nx = (vx - panX) / dispW();
        float ny = (vy - panY) / dispH();
        nx = Math.max(0f, Math.min(1f, nx));
        ny = Math.max(0f, Math.min(1f, ny));
        int seq = nextSeq();
        send(Protocol.obj("t", "mm", "x", (double) nx, "y", (double) ny, "i", seq));
        trackMove(seq, true, Math.round(nx * 65536), Math.round(ny * 65536));
    }

    private void click(int button, int count) {
        send(Protocol.obj("t", "click", "b", button, "n", count));
    }

    private void button(int button, boolean down) {
        send(Protocol.obj("t", "mb", "b", button, "d", down));
    }

    private void cancelPendingClick() {
        if (pendingClick != null) {
            removeCallbacks(pendingClick);
            pendingClick = null;
        }
    }

    // ------------------------------------------------------------ touch

    private static float dist(float ax, float ay, float bx, float by) {
        return (float) Math.hypot(ax - bx, ay - by);
    }

    private static float span(MotionEvent e) {
        if (e.getPointerCount() < 2) return 0;
        return dist(e.getX(0), e.getY(0), e.getX(1), e.getY(1));
    }

    private static float focusX(MotionEvent e) {
        float s = 0;
        int n = Math.min(2, e.getPointerCount());
        for (int i = 0; i < n; i++) s += e.getX(i);
        return s / n;
    }

    private static float focusY(MotionEvent e) {
        float s = 0;
        int n = Math.min(2, e.getPointerCount());
        for (int i = 0; i < n; i++) s += e.getY(i);
        return s / n;
    }

    @Override
    public boolean onTouchEvent(MotionEvent e) {
        switch (e.getActionMasked()) {
            case MotionEvent.ACTION_DOWN: {
                requestFocus();
                maxPointers = 1;
                downTime = e.getEventTime();
                x0 = lastX = e.getX();
                y0 = lastY = e.getY();
                lastMoveTime = downTime;
                moved = false;
                longFired = false;
                fracX = fracY = 0;
                tapDrag = false;
                if (touchpad && pendingClick != null) {
                    // second touch shortly after a tap: either double click or tap-and-drag
                    cancelPendingClick();
                    tapDrag = true;
                } else {
                    postDelayed(longPress, LONG_PRESS_MS);
                }
                break;
            }
            case MotionEvent.ACTION_POINTER_DOWN: {
                removeCallbacks(longPress);
                maxPointers = Math.max(maxPointers, e.getPointerCount());
                if (dragging) {
                    button(0, false);
                    dragging = false;
                }
                if (tapDrag) {
                    // first tap of a double-tap turned into a multi-finger gesture: deliver it
                    click(0, 1);
                    tapDrag = false;
                }
                if (e.getPointerCount() == 2) {
                    twoMode = TWO_UNDECIDED;
                    span0 = Math.max(1f, span(e));
                    focus0X = lastFocusX = focusX(e);
                    focus0Y = lastFocusY = focusY(e);
                    zoom0 = zoom;
                    float s = scale();
                    anchorX = (focus0X - panX) / s;
                    anchorY = (focus0Y - panY) / s;
                    scrollAccX = scrollAccY = 0;
                }
                break;
            }
            case MotionEvent.ACTION_MOVE: {
                if (maxPointers == 1 && e.getPointerCount() == 1) onOneMove(e);
                else if (maxPointers == 2 && e.getPointerCount() == 2) onTwoMove(e);
                break;
            }
            case MotionEvent.ACTION_UP: {
                removeCallbacks(longPress);
                long dur = e.getEventTime() - downTime;
                if (maxPointers == 1) {
                    onOneUp(e);
                } else if (maxPointers == 2) {
                    if (twoMode == TWO_UNDECIDED && dur < 350) {
                        if (!touchpad) moveAbs(focus0X, focus0Y);
                        click(1, 1);
                    } else if (twoMode == TWO_ZOOM && zoom < 1.08f) {
                        zoom = 1f;
                        clampPan();
                        invalidate();
                    }
                } else if (maxPointers >= 3 && dur < 450) {
                    if (sink != null) sink.toggleKeyboard();
                }
                maxPointers = 0;
                break;
            }
            case MotionEvent.ACTION_CANCEL: {
                removeCallbacks(longPress);
                if (dragging) {
                    button(0, false);
                    dragging = false;
                }
                maxPointers = 0;
                break;
            }
            default:
                break;
        }
        return true;
    }

    private void onOneMove(MotionEvent e) {
        float x = e.getX(), y = e.getY();
        if (!moved) {
            if (dist(x, y, x0, y0) < touchSlop) return;
            moved = true;
            removeCallbacks(longPress);
            if (longFired) return;
            if (touchpad) {
                if (tapDrag) {
                    button(0, true);
                    dragging = true;
                }
            } else {
                moveAbs(x0, y0);
                button(0, true);
                dragging = true;
            }
        }
        if (longFired) return;
        long t = e.getEventTime();
        if (touchpad) {
            float dx = (x - lastX) / density, dy = (y - lastY) / density; // dp
            float dt = Math.max(1, t - lastMoveTime);
            float speed = (float) Math.hypot(dx, dy) / dt; // dp per ms
            float gain = sensitivity * (1f + Math.max(0f, Math.min(2.5f, (speed - 0.15f) * 1.6f)));
            fracX += dx * gain;
            fracY += dy * gain;
            int ix = (int) fracX, iy = (int) fracY;
            if (ix != 0 || iy != 0) {
                fracX -= ix;
                fracY -= iy;
                int seq = nextSeq();
                send(Protocol.obj("t", "mr", "dx", ix, "dy", iy, "i", seq));
                trackMove(seq, false, ix, iy);
            }
        } else {
            moveAbs(x, y);
        }
        lastX = x;
        lastY = y;
        lastMoveTime = t;
    }

    private void onOneUp(MotionEvent e) {
        long now = SystemClock.uptimeMillis();
        if (dragging) {
            button(0, false);
            dragging = false;
            tapDrag = false;
            return;
        }
        if (moved || longFired) {
            tapDrag = false;
            return;
        }
        if (touchpad) {
            if (tapDrag) {
                tapDrag = false;
                click(0, 2);
            } else {
                // wait a moment: a second touch may turn this tap into a drag
                pendingClick = () -> {
                    pendingClick = null;
                    click(0, 1);
                };
                postDelayed(pendingClick, DOUBLE_TAP_MS);
            }
        } else {
            float tx = x0, ty = y0;
            if (now - lastTapUp < 400 && dist(tx, ty, lastTapX, lastTapY) < 28 * density) {
                tx = lastTapX; // keep a double tap on exactly the same pixel
                ty = lastTapY;
            }
            moveAbs(tx, ty);
            click(0, 1);
            lastTapUp = now;
            lastTapX = tx;
            lastTapY = ty;
        }
    }

    private void onTwoMove(MotionEvent e) {
        float sp = span(e), fx = focusX(e), fy = focusY(e);
        if (twoMode == TWO_UNDECIDED) {
            if (Math.abs(sp - span0) > touchSlop * 2f) {
                twoMode = TWO_ZOOM;
            } else if (dist(fx, fy, focus0X, focus0Y) > touchSlop) {
                twoMode = TWO_SCROLL;
            }
        }
        if (twoMode == TWO_SCROLL) {
            float notch = 28 * density;
            scrollAccY += (fy - lastFocusY) / notch;
            scrollAccX += (fx - lastFocusX) / notch;
            int ny = (int) scrollAccY, nx = (int) scrollAccX;
            if (ny != 0 || nx != 0) {
                scrollAccY -= ny;
                scrollAccX -= nx;
                // fingers down → content down → wheel up (like a touchscreen)
                send(Protocol.obj("t", "wh", "dy", ny * 120, "dx", -nx * 120));
            }
        } else if (twoMode == TWO_ZOOM && frame != null) {
            zoom = Math.max(1f, Math.min(6f, zoom0 * sp / span0));
            float s = scale();
            if (touchpad && pointerFraction(tmp2)) {
                // touchpad: zoom around the pointer, which stays in the centre
                panX = getWidth() / 2f - tmp2[0] * dispW();
                panY = getHeight() / 2f - tmp2[1] * dispH();
            } else {
                panX = fx - anchorX * s;
                panY = fy - anchorY * s;
            }
            clampPan();
            invalidate();
        }
        lastFocusX = fx;
        lastFocusY = fy;
    }

    // ------------------------------------------------------------ keyboard

    @Override
    public boolean onCheckIsTextEditor() {
        return true;
    }

    @Override
    public InputConnection onCreateInputConnection(EditorInfo out) {
        out.inputType = InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
                | InputType.TYPE_TEXT_FLAG_MULTI_LINE;
        out.imeOptions = EditorInfo.IME_FLAG_NO_EXTRACT_UI | EditorInfo.IME_FLAG_NO_FULLSCREEN
                | EditorInfo.IME_ACTION_NONE;
        out.initialSelStart = 0;
        out.initialSelEnd = 0;
        return new RemoteInputConnection();
    }

    /** Forwards whatever the soft keyboard types, including composing text, as remote keystrokes. */
    private final class RemoteInputConnection extends BaseInputConnection {
        private String composing = "";

        RemoteInputConnection() {
            super(RemoteView.this, false);
        }

        private void replaceComposing(String now) {
            String old = composing;
            int p = 0;
            int max = Math.min(old.length(), now.length());
            while (p < max && old.charAt(p) == now.charAt(p)) p++;
            for (int i = p; i < old.length(); i++) sink.tapKey(Protocol.VK_BACK);
            if (p < now.length()) sink.sendText(now.substring(p));
            composing = now;
        }

        @Override
        public boolean commitText(CharSequence text, int newCursorPosition) {
            if (sink == null) return true;
            replaceComposing(text == null ? "" : text.toString());
            composing = "";
            return true;
        }

        @Override
        public boolean setComposingText(CharSequence text, int newCursorPosition) {
            if (sink == null) return true;
            replaceComposing(text == null ? "" : text.toString());
            return true;
        }

        @Override
        public boolean finishComposingText() {
            composing = "";
            return true;
        }

        @Override
        public boolean deleteSurroundingText(int beforeLength, int afterLength) {
            if (sink == null) return true;
            for (int i = 0; i < beforeLength; i++) sink.tapKey(Protocol.VK_BACK);
            for (int i = 0; i < afterLength; i++) sink.tapKey(Protocol.VK_DELETE);
            return true;
        }

        @Override
        public boolean performEditorAction(int actionCode) {
            if (sink != null) sink.tapKey(Protocol.VK_RETURN);
            return true;
        }

        @Override
        public boolean sendKeyEvent(KeyEvent event) {
            if (sink == null || event.getAction() != KeyEvent.ACTION_DOWN) return true;
            int vk = KeyMap.special(event.getKeyCode());
            if (vk != 0) {
                composing = "";
                sink.tapKey(vk);
                return true;
            }
            int ch = event.getUnicodeChar();
            if (ch != 0) sink.sendText(new String(Character.toChars(ch)));
            return true;
        }
    }

    // hardware keyboard
    @Override
    public boolean onKeyDown(int keyCode, KeyEvent event) {
        return handleHardwareKey(keyCode, event, true) || super.onKeyDown(keyCode, event);
    }

    @Override
    public boolean onKeyUp(int keyCode, KeyEvent event) {
        return handleHardwareKey(keyCode, event, false) || super.onKeyUp(keyCode, event);
    }

    private boolean handleHardwareKey(int keyCode, KeyEvent event, boolean down) {
        if (sink == null) return false;
        if (keyCode == KeyEvent.KEYCODE_BACK || keyCode == KeyEvent.KEYCODE_VOLUME_UP
                || keyCode == KeyEvent.KEYCODE_VOLUME_DOWN || keyCode == KeyEvent.KEYCODE_VOLUME_MUTE) {
            return false;
        }
        int mod = KeyMap.modifier(keyCode);
        if (mod != 0) {
            sink.keyEvent(mod, down);
            return true;
        }
        boolean withMods = event.isCtrlPressed() || event.isAltPressed() || event.isMetaPressed();
        int vk = KeyMap.special(keyCode);
        if (vk == 0 && withMods) vk = KeyMap.letterOrDigit(keyCode);
        if (vk != 0) {
            // ordinary keys are sent as a complete press on key-down: the matching
            // key-up is not always delivered (IME), and a stuck key on the PC is worse
            if (down) sink.tapKey(vk);
            return true;
        }
        if (down) {
            int ch = event.getUnicodeChar();
            if (ch != 0) {
                sink.sendText(new String(Character.toChars(ch)));
                return true;
            }
        }
        return false;
    }
}
