package biz.fesh.remotedesktop;

import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.os.Build;

import org.json.JSONObject;

import java.io.BufferedInputStream;
import java.io.BufferedOutputStream;
import java.io.DataInputStream;
import java.io.EOFException;
import java.io.IOException;
import java.io.OutputStream;
import java.net.ConnectException;
import java.net.InetSocketAddress;
import java.net.NoRouteToHostException;
import java.net.Socket;
import java.net.SocketTimeoutException;
import java.net.UnknownHostException;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;

/**
 * One TCP session with the PC. Callbacks arrive on the background reader thread.
 */
final class Connection {

    interface Listener {
        void onConnected(JSONObject auth);

        void onFrame(Bitmap frame, float cursorX, float cursorY);

        /** Pointer position in physical pixels of the streamed monitor (w×h); seq = last move applied. */
        void onCursor(int x, int y, int w, int h, int seq, boolean visible);

        void onCursorShape(Bitmap shape, int hotX, int hotY);

        void onMessage(JSONObject msg);

        /** error == null means the user closed it. */
        void onDisconnected(String error, boolean authFailed);
    }

    private static final byte[] ACK = Protocol.packet(Protocol.MSG_ACK, null);
    private static final byte[] PING = Protocol.json(Protocol.obj("t", "ping"));

    private final String host;
    private final int port;
    private final String pin;
    private final Listener listener;

    private final LinkedBlockingQueue<byte[]> outQueue = new LinkedBlockingQueue<>();
    private final ArrayBlockingQueue<Bitmap> pool = new ArrayBlockingQueue<>(4);
    private volatile boolean closed;
    private volatile Socket socket;
    private Thread writer;

    Connection(String host, int port, String pin, Listener listener) {
        this.host = host;
        this.port = port;
        this.pin = pin;
        this.listener = listener;
    }

    void start() {
        Thread t = new Thread(this::readLoop, "rd-reader");
        t.setDaemon(true);
        t.start();
    }

    void close() {
        closed = true;
        Socket s = socket;
        if (s != null) {
            try {
                s.close();
            } catch (IOException ignored) {
            }
        }
        if (writer != null) writer.interrupt();
    }

    boolean isClosed() {
        return closed;
    }

    void send(JSONObject msg) {
        if (!closed) outQueue.offer(Protocol.json(msg));
    }

    void sendAck() {
        if (!closed) outQueue.offer(ACK);
    }

    /** Give a bitmap that is no longer displayed back for reuse by the decoder. */
    void recycle(Bitmap b) {
        if (b != null && !b.isRecycled()) pool.offer(b);
    }

    private void readLoop() {
        String error = null;
        boolean authFailed = false;
        try {
            Socket s = new Socket();
            socket = s;
            s.setTcpNoDelay(true);
            s.setReceiveBufferSize(1 << 20);
            s.connect(new InetSocketAddress(host, port), 6000);
            s.setSoTimeout(15000);
            if (closed) return;

            DataInputStream in = new DataInputStream(new BufferedInputStream(s.getInputStream(), 1 << 16));
            OutputStream out = new BufferedOutputStream(s.getOutputStream(), 1 << 14);

            JSONObject hello = Protocol.obj("t", "hello", "pin", pin, "client", Build.MODEL, "ver", Protocol.VERSION);
            out.write(Protocol.json(hello));
            out.flush();

            int type = in.readUnsignedByte();
            int len = in.readInt();
            if (type != Protocol.MSG_JSON || len < 0 || len > (1 << 20)) throw new IOException("bad handshake");
            byte[] data = new byte[len];
            in.readFully(data);
            JSONObject auth = new JSONObject(new String(data, StandardCharsets.UTF_8));
            if (!auth.optBoolean("ok")) {
                authFailed = true;
                error = auth.optString("msg", "Доступ заборонено");
                return;
            }

            writer = new Thread(() -> writeLoop(out), "rd-writer");
            writer.setDaemon(true);
            writer.start();
            listener.onConnected(auth);

            byte[] buf = new byte[1 << 20];
            while (!closed) {
                type = in.readUnsignedByte();
                len = in.readInt();
                if (len < 0 || len > (16 << 20)) throw new IOException("bad length");
                if (buf.length < len) buf = new byte[len + (64 << 10)];
                in.readFully(buf, 0, len);
                if (type == Protocol.MSG_FRAME && len > 8) {
                    float cx = Protocol.readFloat(buf, 0);
                    float cy = Protocol.readFloat(buf, 4);
                    Bitmap bmp = decode(buf, 8, len - 8);
                    if (bmp != null) listener.onFrame(bmp, cx, cy);
                    else sendAck();
                } else if (type == Protocol.MSG_CURSOR && len >= 21) {
                    listener.onCursor(Protocol.readInt(buf, 0), Protocol.readInt(buf, 4), Protocol.readInt(buf, 8),
                            Protocol.readInt(buf, 12), Protocol.readInt(buf, 16), (buf[20] & 1) != 0);
                } else if (type == Protocol.MSG_SHAPE && len > 4) {
                    Bitmap shape = BitmapFactory.decodeByteArray(buf, 4, len - 4);
                    if (shape != null) listener.onCursorShape(shape, Protocol.readUShort(buf, 0), Protocol.readUShort(buf, 2));
                } else if (type == Protocol.MSG_JSON) {
                    try {
                        listener.onMessage(new JSONObject(new String(buf, 0, len, StandardCharsets.UTF_8)));
                    } catch (Exception ignored) {
                    }
                }
            }
        } catch (SocketTimeoutException e) {
            error = "Комп'ютер не відповідає";
        } catch (ConnectException | NoRouteToHostException e) {
            error = "Не вдалося підключитися до " + host + ":" + port
                    + ".\nПеревірте, що сервер запущено і телефон у тій самій мережі.";
        } catch (UnknownHostException e) {
            error = "Невідома адреса: " + host;
        } catch (EOFException e) {
            error = "Комп'ютер закрив з'єднання";
        } catch (Exception e) {
            error = "З'єднання втрачено";
        } finally {
            boolean byUser = closed;
            close();
            listener.onDisconnected(byUser ? null : (error == null ? "З'єднання втрачено" : error), authFailed);
        }
    }

    private void writeLoop(OutputStream out) {
        try {
            while (!closed) {
                byte[] m = outQueue.poll(2500, TimeUnit.MILLISECONDS);
                if (m == null) m = PING;
                out.write(m);
                byte[] next;
                while ((next = outQueue.poll()) != null) out.write(next);
                out.flush();
            }
        } catch (InterruptedException ignored) {
        } catch (IOException e) {
            Socket s = socket;
            if (s != null) {
                try {
                    s.close();
                } catch (IOException ignored) {
                }
            }
        }
    }

    private Bitmap decode(byte[] b, int off, int len) {
        BitmapFactory.Options o = new BitmapFactory.Options();
        o.inJustDecodeBounds = true;
        BitmapFactory.decodeByteArray(b, off, len, o);
        int w = o.outWidth, h = o.outHeight;
        if (w <= 0 || h <= 0) return null;

        Bitmap reuse = null;
        Bitmap cand;
        while ((cand = pool.poll()) != null) {
            if (!cand.isRecycled() && cand.getWidth() == w && cand.getHeight() == h) {
                reuse = cand;
                break;
            }
        }
        o.inJustDecodeBounds = false;
        o.inMutable = true;
        o.inPreferredConfig = Bitmap.Config.ARGB_8888;
        o.inBitmap = reuse;
        try {
            return BitmapFactory.decodeByteArray(b, off, len, o);
        } catch (IllegalArgumentException e) {
            o.inBitmap = null;
            return BitmapFactory.decodeByteArray(b, off, len, o);
        }
    }
}
