package biz.fesh.remotedesktop;

import android.content.Context;
import android.content.SharedPreferences;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;

final class Prefs {
    static final String MODE_TOUCHPAD = "touchpad";
    static final String MODE_DIRECT = "direct";

    private final SharedPreferences sp;

    Prefs(Context ctx) {
        sp = ctx.getSharedPreferences("rd", Context.MODE_PRIVATE);
    }

    int quality() { return sp.getInt("quality", 60); }
    int maxWidth() { return sp.getInt("maxw", 1280); }
    int fps() { return sp.getInt("fps", 30); }
    int sensitivity() { return sp.getInt("sens", 100); } // percent
    String mode() { return sp.getString("mode", MODE_TOUCHPAD); }
    boolean helpShown() { return sp.getBoolean("help_shown", false); }

    void setQuality(int v) { sp.edit().putInt("quality", v).apply(); }
    void setMaxWidth(int v) { sp.edit().putInt("maxw", v).apply(); }
    void setFps(int v) { sp.edit().putInt("fps", v).apply(); }
    void setSensitivity(int v) { sp.edit().putInt("sens", v).apply(); }
    void setMode(String v) { sp.edit().putString("mode", v).apply(); }
    void setHelpShown() { sp.edit().putBoolean("help_shown", true).apply(); }

    String pin(String host, int port) { return sp.getString("pin_" + host + ":" + port, null); }

    void setPin(String host, int port, String pin) {
        if (pin == null) sp.edit().remove("pin_" + host + ":" + port).apply();
        else sp.edit().putString("pin_" + host + ":" + port, pin).apply();
    }

    static final class Host {
        final String name, host;
        final int port;

        Host(String name, String host, int port) {
            this.name = name;
            this.host = host;
            this.port = port;
        }

        String key() { return host + ":" + port; }
    }

    List<Host> recent() {
        List<Host> out = new ArrayList<>();
        try {
            JSONArray a = new JSONArray(sp.getString("recent", "[]"));
            for (int i = 0; i < a.length(); i++) {
                JSONObject o = a.getJSONObject(i);
                out.add(new Host(o.optString("name"), o.optString("host"), o.optInt("port", Protocol.DEFAULT_PORT)));
            }
        } catch (Exception ignored) {
        }
        return out;
    }

    void addRecent(Host h) {
        List<Host> list = recent();
        JSONArray a = new JSONArray();
        a.put(Protocol.obj("name", h.name, "host", h.host, "port", h.port));
        int n = 1;
        for (Host o : list) {
            if (o.key().equals(h.key())) continue;
            if (n++ >= 6) break;
            a.put(Protocol.obj("name", o.name, "host", o.host, "port", o.port));
        }
        sp.edit().putString("recent", a.toString()).apply();
    }

    void removeRecent(Host h) {
        JSONArray a = new JSONArray();
        for (Host o : recent()) {
            if (o.key().equals(h.key())) continue;
            a.put(Protocol.obj("name", o.name, "host", o.host, "port", o.port));
        }
        sp.edit().putString("recent", a.toString()).apply();
        setPin(h.host, h.port, null);
    }
}
