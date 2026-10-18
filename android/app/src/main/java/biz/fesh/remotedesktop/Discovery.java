package biz.fesh.remotedesktop;

import android.os.Handler;
import android.os.Looper;

import org.json.JSONObject;

import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.InterfaceAddress;
import java.net.NetworkInterface;
import java.net.SocketTimeoutException;
import java.nio.charset.StandardCharsets;
import java.util.Collections;
import java.util.HashSet;
import java.util.LinkedHashSet;
import java.util.Set;

/** Finds PCs running the server by UDP broadcast. Callbacks run on the UI thread. */
final class Discovery {

    interface Callback {
        void onFound(String name, String host, int port);

        void onDone();
    }

    private final Handler ui = new Handler(Looper.getMainLooper());
    private volatile boolean cancelled;

    void cancel() {
        cancelled = true;
    }

    void search(Callback cb) {
        Thread t = new Thread(() -> run(cb), "rd-discovery");
        t.setDaemon(true);
        t.start();
    }

    private void run(Callback cb) {
        Set<String> seen = new HashSet<>();
        try (DatagramSocket s = new DatagramSocket()) {
            s.setBroadcast(true);
            s.setSoTimeout(300);
            byte[] q = Protocol.DISCOVERY_QUERY.getBytes(StandardCharsets.US_ASCII);
            Set<InetAddress> targets = broadcastAddresses();
            long end = System.currentTimeMillis() + 3000;
            long nextSend = 0;
            byte[] buf = new byte[1024];
            while (!cancelled && System.currentTimeMillis() < end) {
                if (System.currentTimeMillis() >= nextSend) {
                    for (InetAddress a : targets) {
                        try {
                            s.send(new DatagramPacket(q, q.length, a, Protocol.DISCOVERY_PORT));
                        } catch (Exception ignored) {
                        }
                    }
                    nextSend = System.currentTimeMillis() + 900;
                }
                try {
                    DatagramPacket p = new DatagramPacket(buf, buf.length);
                    s.receive(p);
                    JSONObject o = new JSONObject(new String(p.getData(), 0, p.getLength(), StandardCharsets.UTF_8));
                    if (!"rdesk".equals(o.optString("t"))) continue;
                    String host = p.getAddress().getHostAddress();
                    int port = o.optInt("port", Protocol.DEFAULT_PORT);
                    String name = o.optString("name", host);
                    if (seen.add(host + ":" + port)) ui.post(() -> cb.onFound(name, host, port));
                } catch (SocketTimeoutException ignored) {
                } catch (Exception ignored) {
                }
            }
        } catch (Exception ignored) {
        }
        ui.post(cb::onDone);
    }

    private static Set<InetAddress> broadcastAddresses() {
        Set<InetAddress> out = new LinkedHashSet<>();
        try {
            for (NetworkInterface ni : Collections.list(NetworkInterface.getNetworkInterfaces())) {
                if (!ni.isUp() || ni.isLoopback()) continue;
                for (InterfaceAddress ia : ni.getInterfaceAddresses()) {
                    if (ia.getAddress() instanceof Inet4Address && ia.getBroadcast() != null) {
                        out.add(ia.getBroadcast());
                    }
                }
            }
        } catch (Exception ignored) {
        }
        try {
            out.add(InetAddress.getByName("255.255.255.255"));
        } catch (Exception ignored) {
        }
        return out;
    }
}
