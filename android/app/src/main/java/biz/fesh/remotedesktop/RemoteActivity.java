package biz.fesh.remotedesktop;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.Context;
import android.content.Intent;
import android.graphics.Bitmap;
import android.graphics.Color;
import android.graphics.Insets;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.view.WindowInsetsController;
import android.view.WindowManager;
import android.view.inputmethod.InputMethodManager;
import android.widget.FrameLayout;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import android.widget.RadioButton;
import android.widget.RadioGroup;
import android.widget.ScrollView;
import android.widget.SeekBar;
import android.widget.TextView;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;

public class RemoteActivity extends Activity implements RemoteView.InputSink {

    static final String EXTRA_HOST = "host", EXTRA_PORT = "port", EXTRA_NAME = "name", EXTRA_PIN = "pin", EXTRA_ERROR = "error";
    static final int RESULT_AUTH_FAILED = RESULT_FIRST_USER + 1;

    private final Handler ui = new Handler(Looper.getMainLooper());
    private Prefs prefs;
    private String host, name, pin;
    private int port;

    private Connection conn;
    private int generation;
    private boolean connected;

    private LinearLayout root;
    private RemoteView remote;
    private TextView status;
    private LinearLayout toolbarBox;
    private HorizontalScrollView extraScroll;
    private TextView fabShow, btnMode, btnMon, btnZoom;
    private AlertDialog lostDialog;

    private final Map<Integer, TextView> stickyButtons = new HashMap<>();
    private final Set<Integer> sticky = new LinkedHashSet<>();
    private int monCount = 1, monIndex = 0;
    private JSONArray monList, virtualScreen;
    private boolean keyboardVisible;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        prefs = new Prefs(this);
        Intent i = getIntent();
        host = i.getStringExtra(EXTRA_HOST);
        port = i.getIntExtra(EXTRA_PORT, Protocol.DEFAULT_PORT);
        name = i.getStringExtra(EXTRA_NAME);
        pin = i.getStringExtra(EXTRA_PIN);
        if (name == null) name = host;

        getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        buildUi();
        hideSystemBars();
        connect();
    }

    // ------------------------------------------------------------ UI

    private int dp(float v) {
        return Math.round(v * getResources().getDisplayMetrics().density);
    }

    private void buildUi() {
        root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(Color.BLACK);

        FrameLayout stage = new FrameLayout(this);
        root.addView(stage, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f));

        remote = new RemoteView(this);
        remote.setTouchpad(Prefs.MODE_TOUCHPAD.equals(prefs.mode()));
        remote.setSensitivity(prefs.sensitivity() / 100f);
        stage.addView(remote, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));

        status = new TextView(this);
        status.setTextColor(0xFFCBD5E1);
        status.setTextSize(16);
        status.setGravity(Gravity.CENTER);
        status.setPadding(dp(24), dp(24), dp(24), dp(24));
        stage.addView(status, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT, Gravity.CENTER));

        fabShow = new TextView(this);
        fabShow.setText("☰");
        fabShow.setTextColor(Color.WHITE);
        fabShow.setTextSize(18);
        fabShow.setGravity(Gravity.CENTER);
        fabShow.setBackgroundResource(R.drawable.fab);
        fabShow.setVisibility(View.GONE);
        fabShow.setOnClickListener(v -> setToolbarVisible(true));
        FrameLayout.LayoutParams fp = new FrameLayout.LayoutParams(dp(42), dp(42), Gravity.END | Gravity.CENTER_VERTICAL);
        fp.setMarginEnd(dp(6));
        stage.addView(fabShow, fp);

        toolbarBox = new LinearLayout(this);
        toolbarBox.setOrientation(LinearLayout.VERTICAL);
        toolbarBox.setBackgroundResource(R.drawable.toolbar_bg);
        root.addView(toolbarBox, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));

        LinearLayout extra = newRow();
        extraScroll = wrapRow(extra);
        extraScroll.setVisibility(View.GONE);
        toolbarBox.addView(extraScroll);
        LinearLayout mainLine = new LinearLayout(this);
        mainLine.setOrientation(LinearLayout.HORIZONTAL);
        toolbarBox.addView(mainLine);
        LinearLayout main = newRow();
        mainLine.addView(wrapRow(main), new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f));
        LinearLayout fixed = newRow();
        mainLine.addView(fixed);

        // main row
        main.addView(button("⌨", v -> toggleKeyboard()));
        btnMode = button("", v -> toggleMode());
        updateModeButton();
        main.addView(btnMode);
        // zoom: 100% → 150% → 200% → 300% → 100%; pinch with two fingers for any level
        btnZoom = button("🔍 100%", v -> remote.cycleZoom());
        btnZoom.setOnLongClickListener(v -> {
            remote.resetZoom();
            return true;
        });
        main.addView(btnZoom);
        remote.setZoomListener(this::updateZoomButton);
        main.addView(button("Esc", v -> tapKey(Protocol.VK_ESCAPE)));
        main.addView(button("Tab", v -> tapKey(Protocol.VK_TAB)));
        main.addView(stickyButton("Ctrl", Protocol.VK_CONTROL));
        main.addView(stickyButton("Alt", Protocol.VK_MENU));
        main.addView(stickyButton("Shift", Protocol.VK_SHIFT));
        main.addView(stickyButton("Win", Protocol.VK_LWIN));
        main.addView(repeatButton("←", Protocol.VK_LEFT));
        main.addView(repeatButton("↑", Protocol.VK_UP));
        main.addView(repeatButton("↓", Protocol.VK_DOWN));
        main.addView(repeatButton("→", Protocol.VK_RIGHT));
        main.addView(repeatButton("⌫", Protocol.VK_BACK));
        main.addView(repeatButton("Del", Protocol.VK_DELETE));
        main.addView(button("Enter", v -> tapKey(Protocol.VK_RETURN)));
        btnMon = button("🖥 1", v -> nextMonitor());
        btnMon.setVisibility(View.GONE);
        main.addView(btnMon);
        // always visible on the right, even on a narrow portrait screen
        fixed.addView(button("⋯", v -> extraScroll.setVisibility(extraScroll.getVisibility() == View.VISIBLE ? View.GONE : View.VISIBLE)));
        fixed.addView(button("⚙", v -> showSettings()));
        fixed.addView(button("✕", v -> setToolbarVisible(false)));

        // extra row
        extra.addView(button("100%", v -> remote.resetZoom()));
        for (int f = 0; f < 12; f++) {
            final int vk = Protocol.VK_F1 + f;
            extra.addView(button("F" + (f + 1), v -> tapKey(vk)));
        }
        extra.addView(button("Home", v -> tapKey(Protocol.VK_HOME)));
        extra.addView(button("End", v -> tapKey(Protocol.VK_END)));
        extra.addView(repeatButton("PgUp", Protocol.VK_PRIOR));
        extra.addView(repeatButton("PgDn", Protocol.VK_NEXT));
        extra.addView(button("Ins", v -> tapKey(Protocol.VK_INSERT)));
        extra.addView(button("PrtSc", v -> tapKey(Protocol.VK_SNAPSHOT)));
        extra.addView(button("ПКМ", v -> send(Protocol.obj("t", "click", "b", 1, "n", 1))));
        extra.addView(button("СКМ", v -> send(Protocol.obj("t", "click", "b", 2, "n", 1))));
        extra.addView(button("Alt+Tab", v -> combo(Protocol.VK_MENU, Protocol.VK_TAB)));
        extra.addView(button("Alt+F4", v -> combo(Protocol.VK_MENU, Protocol.VK_F1 + 3)));
        extra.addView(button("Win+D", v -> combo(Protocol.VK_LWIN, 'D')));
        extra.addView(button("Ctrl+C", v -> combo(Protocol.VK_CONTROL, 'C')));
        extra.addView(button("Ctrl+V", v -> combo(Protocol.VK_CONTROL, 'V')));
        extra.addView(button("Ctrl+X", v -> combo(Protocol.VK_CONTROL, 'X')));
        extra.addView(button("Ctrl+Z", v -> combo(Protocol.VK_CONTROL, 'Z')));
        extra.addView(button("Ctrl+A", v -> combo(Protocol.VK_CONTROL, 'A')));
        extra.addView(button("Ctrl+S", v -> combo(Protocol.VK_CONTROL, 'S')));
        extra.addView(button("Диспетчер задач", v -> combo(Protocol.VK_CONTROL, Protocol.VK_SHIFT, Protocol.VK_ESCAPE)));
        extra.addView(button("🔇", v -> tapKey(Protocol.VK_VOLUME_MUTE)));
        extra.addView(repeatButton("🔉", Protocol.VK_VOLUME_DOWN));
        extra.addView(repeatButton("🔊", Protocol.VK_VOLUME_UP));
        extra.addView(button("⏮", v -> tapKey(Protocol.VK_MEDIA_PREV)));
        extra.addView(button("⏯", v -> tapKey(Protocol.VK_MEDIA_PLAY_PAUSE)));
        extra.addView(button("⏭", v -> tapKey(Protocol.VK_MEDIA_NEXT)));
        extra.addView(button("?", v -> showHelp()));

        setContentView(root);

        if (Build.VERSION.SDK_INT >= 30) {
            root.setOnApplyWindowInsetsListener((v, insets) -> {
                Insets cut = insets.getInsets(WindowInsets.Type.displayCutout());
                Insets ime = insets.getInsets(WindowInsets.Type.ime());
                keyboardVisible = insets.isVisible(WindowInsets.Type.ime());
                v.setPadding(cut.left, cut.top, cut.right, Math.max(cut.bottom, ime.bottom));
                return WindowInsets.CONSUMED;
            });
        }
        status.setText("Підключення до " + name + "…");
    }

    private LinearLayout newRow() {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setPadding(dp(4), 0, dp(4), 0);
        return row;
    }

    private HorizontalScrollView wrapRow(LinearLayout row) {
        HorizontalScrollView sv = new HorizontalScrollView(this);
        sv.setHorizontalScrollBarEnabled(false);
        sv.setOverScrollMode(View.OVER_SCROLL_NEVER);
        sv.addView(row);
        return sv;
    }

    private TextView button(String label, View.OnClickListener l) {
        TextView b = new TextView(this);
        b.setText(label);
        b.setTextColor(Color.WHITE);
        b.setTextSize(15);
        b.setGravity(Gravity.CENTER);
        b.setMinWidth(dp(46));
        b.setPadding(dp(12), 0, dp(12), 0);
        b.setBackgroundResource(R.drawable.btn);
        b.setSingleLine(true);
        if (l != null) b.setOnClickListener(l);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(42));
        lp.setMargins(dp(3), dp(5), dp(3), dp(5));
        b.setLayoutParams(lp);
        return b;
    }

    private TextView stickyButton(String label, int vk) {
        TextView b = button(label, v -> toggleSticky(vk));
        stickyButtons.put(vk, b);
        return b;
    }

    @SuppressLint("ClickableViewAccessibility")
    private TextView repeatButton(String label, int vk) {
        TextView b = button(label, null);
        final boolean[] repeating = new boolean[1];
        final Runnable[] repeat = new Runnable[1];
        repeat[0] = () -> {
            repeating[0] = true;
            tapKey(vk);
            ui.postDelayed(repeat[0], 55);
        };
        // tap on release, auto-repeat while held; a toolbar scroll (CANCEL) sends nothing
        b.setOnTouchListener((v, e) -> {
            switch (e.getActionMasked()) {
                case MotionEvent.ACTION_DOWN:
                    v.setPressed(true);
                    repeating[0] = false;
                    ui.postDelayed(repeat[0], 380);
                    return true;
                case MotionEvent.ACTION_UP:
                    v.setPressed(false);
                    ui.removeCallbacks(repeat[0]);
                    if (!repeating[0]) tapKey(vk);
                    return true;
                case MotionEvent.ACTION_CANCEL:
                    v.setPressed(false);
                    ui.removeCallbacks(repeat[0]);
                    return true;
                default:
                    return true;
            }
        });
        return b;
    }

    private void setToolbarVisible(boolean visible) {
        toolbarBox.setVisibility(visible ? View.VISIBLE : View.GONE);
        fabShow.setVisibility(visible ? View.GONE : View.VISIBLE);
    }

    private void updateZoomButton(float z) {
        if (btnZoom != null) btnZoom.setText("🔍 " + Math.round(z * 10) * 10 + "%");
    }

    private void updateModeButton() {
        btnMode.setText(remote.isTouchpad() ? "🖱 Тачпад" : "👆 Дотик");
    }

    private void toggleMode() {
        boolean tp = !remote.isTouchpad();
        remote.setTouchpad(tp);
        prefs.setMode(tp ? Prefs.MODE_TOUCHPAD : Prefs.MODE_DIRECT);
        updateModeButton();
        Toast.makeText(this, tp ? "Тачпад: водіть пальцем, як по тачпаду ноутбука"
                : "Дотик: курсор переходить туди, де ви торкнулися", Toast.LENGTH_SHORT).show();
    }

    private void updateMonitorButton() {
        btnMon.setVisibility(monCount > 1 ? View.VISIBLE : View.GONE);
        btnMon.setText("🖥 " + (monIndex + 1) + "/" + monCount);
    }

    private void nextMonitor() {
        if (monCount < 2) return;
        monIndex = (monIndex + 1) % monCount;
        updatePointerBounds();
        updateMonitorButton();
        remote.resetZoom();
        sendConfig();
    }

    /** The pointer can leave the streamed monitor only within the whole virtual screen. */
    private void updatePointerBounds() {
        JSONObject m = monList != null ? monList.optJSONObject(monIndex) : null;
        if (m == null || virtualScreen == null || virtualScreen.length() < 4 || !m.has("x")) {
            remote.setPointerBounds(0, 0, -1, -1); // unknown: the monitor itself
            return;
        }
        int mx = m.optInt("x"), my = m.optInt("y");
        int vx = virtualScreen.optInt(0), vy = virtualScreen.optInt(1), vw = virtualScreen.optInt(2), vh = virtualScreen.optInt(3);
        remote.setPointerBounds(vx - mx, vy - my, vx + vw - 1 - mx, vy + vh - 1 - my);
    }

    private void hideSystemBars() {
        if (Build.VERSION.SDK_INT >= 30) {
            getWindow().setDecorFitsSystemWindows(false);
            WindowInsetsController c = getWindow().getInsetsController();
            if (c != null) {
                c.hide(WindowInsets.Type.systemBars());
                c.setSystemBarsBehavior(WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE);
            }
        } else {
            getWindow().getDecorView().setSystemUiVisibility(View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                    | View.SYSTEM_UI_FLAG_FULLSCREEN | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION);
        }
    }

    @Override
    public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        if (hasFocus) hideSystemBars();
    }

    // ------------------------------------------------------------ connection

    private void connect() {
        if (conn != null) conn.close();
        connected = false;
        final int gen = ++generation;
        status.setVisibility(View.VISIBLE);
        status.setText("Підключення до " + name + "…");
        Connection c = new Connection(host, port, pin, new Connection.Listener() {
            @Override
            public void onConnected(JSONObject auth) {
                ui.post(() -> {
                    if (gen == generation) onConnectedUi(auth);
                });
            }

            @Override
            public void onFrame(Bitmap frame, float cx, float cy) {
                ui.post(() -> {
                    if (gen != generation || isFinishing()) return;
                    remote.setFrame(frame, cx, cy);
                    if (status.getVisibility() == View.VISIBLE) status.setVisibility(View.GONE);
                });
            }

            @Override
            public void onCursor(int x, int y, int w, int h, int seq, boolean visible) {
                ui.post(() -> {
                    if (gen == generation) remote.setCursor(x, y, w, h, seq, visible);
                });
            }

            @Override
            public void onCursorShape(Bitmap shape, int hotX, int hotY) {
                ui.post(() -> {
                    if (gen == generation) remote.setCursorShape(shape, hotX, hotY);
                });
            }

            @Override
            public void onMessage(JSONObject msg) {
                ui.post(() -> {
                    if (gen == generation) onMessageUi(msg);
                });
            }

            @Override
            public void onDisconnected(String error, boolean authFailed) {
                ui.post(() -> {
                    if (gen == generation) onDisconnectedUi(error, authFailed);
                });
            }
        });
        conn = c;
        remote.setSink(this, c);
        c.start();
    }

    private void onConnectedUi(JSONObject auth) {
        connected = true;
        String hostName = auth.optString("host", "");
        if (!hostName.isEmpty()) name = hostName;
        prefs.setPin(host, port, pin);
        prefs.addRecent(new Prefs.Host(name, host, port));
        remote.setCursorChannel(auth.optInt("ver", 1) >= 2);
        JSONArray mons = auth.optJSONArray("mons");
        monCount = mons != null ? Math.max(1, mons.length()) : 1;
        if (monIndex >= monCount) monIndex = 0;
        monList = mons;
        virtualScreen = auth.optJSONArray("vs");
        updatePointerBounds();
        updateMonitorButton();
        sendConfig();
        status.setText("Отримання зображення…");
        if (!prefs.helpShown()) {
            prefs.setHelpShown();
            showHelp();
        }
    }

    private void onMessageUi(JSONObject msg) {
        String t = msg.optString("t");
        if ("mons".equals(t)) {
            JSONArray mons = msg.optJSONArray("mons");
            monCount = mons != null ? Math.max(1, mons.length()) : 1;
            if (monIndex >= monCount) {
                monIndex = 0;
                sendConfig();
            }
            monList = mons;
            if (msg.has("vs")) virtualScreen = msg.optJSONArray("vs");
            updatePointerBounds();
            updateMonitorButton();
        } else if ("error".equals(t)) {
            Toast.makeText(this, msg.optString("msg"), Toast.LENGTH_LONG).show();
        }
    }

    private void onDisconnectedUi(String error, boolean authFailed) {
        if (error == null || isFinishing()) return;
        if (authFailed) {
            Intent data = new Intent();
            data.putExtra(EXTRA_ERROR, error);
            setResult(RESULT_AUTH_FAILED, data);
            finish();
            return;
        }
        if (lostDialog != null && lostDialog.isShowing()) return;
        lostDialog = new AlertDialog.Builder(this, R.style.Dialog)
                .setTitle(connected ? "З'єднання розірвано" : "Не вдалося підключитися")
                .setMessage(error)
                .setCancelable(false)
                .setPositiveButton("Повторити", (d, w) -> connect())
                .setNegativeButton("Вийти", (d, w) -> finish())
                .show();
    }

    private void sendConfig() {
        send(Protocol.obj("t", "cfg", "q", prefs.quality(), "maxw", prefs.maxWidth(), "fps", prefs.fps(), "mon", monIndex));
    }

    @Override
    protected void onDestroy() {
        super.onDestroy();
        generation++;
        ui.removeCallbacksAndMessages(null);
        if (conn != null) conn.close();
        if (!sticky.isEmpty()) sticky.clear();
    }

    @Override
    public void onBackPressed() {
        new AlertDialog.Builder(this, R.style.Dialog)
                .setMessage("Відключитися від " + name + "?")
                .setPositiveButton("Відключитися", (d, w) -> finish())
                .setNegativeButton("Скасувати", null)
                .show();
    }

    // ------------------------------------------------------------ input sink

    @Override
    public void send(JSONObject msg) {
        if (conn != null && connected) conn.send(msg);
    }

    @Override
    public void sendText(String text) {
        if (text == null || text.isEmpty()) return;
        send(Protocol.obj("t", "text", "s", text));
        releaseSticky();
    }

    @Override
    public void tapKey(int vk) {
        send(Protocol.obj("t", "tap", "vk", vk));
        if (!Protocol.isModifier(vk)) releaseSticky();
    }

    @Override
    public void keyEvent(int vk, boolean down) {
        send(Protocol.obj("t", "key", "vk", vk, "d", down));
    }

    private void combo(int... vks) {
        JSONArray a = new JSONArray();
        for (int vk : vks) a.put(vk);
        send(Protocol.obj("t", "combo", "vks", a));
        releaseSticky();
    }

    private void toggleSticky(int vk) {
        TextView b = stickyButtons.get(vk);
        if (sticky.remove(vk)) {
            keyEvent(vk, false);
            if (b != null) b.setBackgroundResource(R.drawable.btn);
        } else {
            sticky.add(vk);
            keyEvent(vk, true);
            if (b != null) b.setBackgroundResource(R.drawable.btn_active);
        }
    }

    private void releaseSticky() {
        if (sticky.isEmpty()) return;
        List<Integer> held = new ArrayList<>(sticky);
        sticky.clear();
        for (int i = held.size() - 1; i >= 0; i--) {
            int vk = held.get(i);
            keyEvent(vk, false);
            TextView b = stickyButtons.get(vk);
            if (b != null) b.setBackgroundResource(R.drawable.btn);
        }
    }

    @Override
    public void toggleKeyboard() {
        InputMethodManager imm = (InputMethodManager) getSystemService(Context.INPUT_METHOD_SERVICE);
        if (keyboardVisible) {
            if (Build.VERSION.SDK_INT >= 30 && getWindow().getInsetsController() != null) {
                getWindow().getInsetsController().hide(WindowInsets.Type.ime());
            } else if (imm != null) {
                imm.hideSoftInputFromWindow(remote.getWindowToken(), 0);
            }
            if (Build.VERSION.SDK_INT < 30) keyboardVisible = false;
        } else {
            remote.requestFocus();
            if (imm != null) imm.restartInput(remote);
            if (Build.VERSION.SDK_INT >= 30 && getWindow().getInsetsController() != null) {
                getWindow().getInsetsController().show(WindowInsets.Type.ime());
            } else if (imm != null) {
                imm.showSoftInput(remote, 0);
            }
            if (Build.VERSION.SDK_INT < 30) keyboardVisible = true;
        }
    }

    // ------------------------------------------------------------ dialogs

    private TextView label(String s) {
        TextView t = new TextView(this);
        t.setText(s);
        t.setTextColor(0xFFF1F5F9);
        t.setTextSize(15);
        t.setPadding(0, dp(14), 0, dp(4));
        return t;
    }

    private RadioGroup radios(String[] labels, int[] values, int current) {
        RadioGroup g = new RadioGroup(this);
        g.setOrientation(RadioGroup.VERTICAL);
        for (int i = 0; i < labels.length; i++) {
            RadioButton r = new RadioButton(this);
            r.setText(labels[i]);
            r.setId(View.generateViewId());
            r.setTag(values[i]);
            g.addView(r);
            if (values[i] == current) r.setChecked(true);
        }
        return g;
    }

    private static int checkedValue(RadioGroup g, int def) {
        View v = g.findViewById(g.getCheckedRadioButtonId());
        return v != null && v.getTag() instanceof Integer ? (Integer) v.getTag() : def;
    }

    private void showSettings() {
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setPadding(dp(22), dp(4), dp(22), dp(8));

        TextView qLabel = label("");
        SeekBar q = new SeekBar(this);
        q.setMax(70);
        q.setProgress(prefs.quality() - 20);
        qLabel.setText("Якість зображення: " + prefs.quality() + "%");
        q.setOnSeekBarChangeListener(new SimpleSeek(p -> qLabel.setText("Якість зображення: " + (p + 20) + "%")));
        box.addView(qLabel);
        box.addView(q);

        box.addView(label("Роздільність"));
        RadioGroup res = radios(new String[]{"960 px — найшвидше", "1280 px — HD (рекомендовано)", "1920 px — Full HD", "Оригінальна (найкраща якість)"},
                new int[]{960, 1280, 1920, 0}, prefs.maxWidth());
        box.addView(res);

        box.addView(label("Кадрів на секунду"));
        RadioGroup fps = radios(new String[]{"15", "30", "60"}, new int[]{15, 30, 60}, prefs.fps());
        fps.setOrientation(RadioGroup.HORIZONTAL);
        box.addView(fps);

        TextView sLabel = label("");
        SeekBar sens = new SeekBar(this);
        sens.setMax(270);
        sens.setProgress(prefs.sensitivity() - 30);
        sLabel.setText("Швидкість курсора (тачпад): " + prefs.sensitivity() + "%");
        sens.setOnSeekBarChangeListener(new SimpleSeek(p -> sLabel.setText("Швидкість курсора (тачпад): " + (p + 30) + "%")));
        box.addView(sLabel);
        box.addView(sens);

        ScrollView sv = new ScrollView(this);
        sv.addView(box);

        new AlertDialog.Builder(this, R.style.Dialog)
                .setTitle("Налаштування")
                .setView(sv)
                .setPositiveButton("Зберегти", (d, w) -> {
                    prefs.setQuality(q.getProgress() + 20);
                    prefs.setMaxWidth(checkedValue(res, 1280));
                    prefs.setFps(checkedValue(fps, 30));
                    prefs.setSensitivity(sens.getProgress() + 30);
                    remote.setSensitivity(prefs.sensitivity() / 100f);
                    sendConfig();
                })
                .setNegativeButton("Скасувати", null)
                .show();
    }

    private void showHelp() {
        String text = "Режим «🖱 Тачпад»\n"
                + "• Ведіть пальцем — рух курсора\n"
                + "• Дотик — клік лівою кнопкою\n"
                + "• Подвійний дотик — подвійний клік\n"
                + "• Подвійний дотик і ведення — перетягування\n"
                + "• Довге натискання або дотик двома пальцями — права кнопка\n"
                + "• Два пальці вгору/вниз — прокрутка\n"
                + "• Швидкий свайп і відпускання — курсор ще трохи їде за інерцією (дотик зупиняє його); якщо затримати палець перед відпусканням — інерції немає\n"
                + "• Розведення двох пальців — збільшення (до 600%); курсор тоді тримається в центрі екрана\n"
                + "• Кнопка «🔍» — 100% → 150% → 200% → 300%, довге натискання — назад до 100%\n\n"
                + "Режим «👆 Дотик»\n"
                + "• Курсор переходить туди, де ви торкнулися\n"
                + "• Ведення пальцем — перетягування\n\n"
                + "Загальне\n"
                + "• Дотик трьома пальцями — клавіатура\n"
                + "• Ctrl, Alt, Shift, Win залишаються натиснутими до наступної клавіші\n"
                + "• «⋯» — F1–F12, гарячі клавіші, гучність, музика\n"
                + "• «✕» ховає панель, «☰» повертає її";
        new AlertDialog.Builder(this, R.style.Dialog)
                .setTitle("Як керувати")
                .setMessage(text)
                .setPositiveButton("Зрозуміло", null)
                .show();
    }

    private static final class SimpleSeek implements SeekBar.OnSeekBarChangeListener {
        interface OnChange {
            void changed(int progress);
        }

        private final OnChange cb;

        SimpleSeek(OnChange cb) {
            this.cb = cb;
        }

        @Override
        public void onProgressChanged(SeekBar seekBar, int progress, boolean fromUser) {
            cb.changed(progress);
        }

        @Override
        public void onStartTrackingTouch(SeekBar seekBar) {
        }

        @Override
        public void onStopTrackingTouch(SeekBar seekBar) {
        }
    }
}
