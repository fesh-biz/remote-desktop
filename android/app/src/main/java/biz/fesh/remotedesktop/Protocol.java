package biz.fesh.remotedesktop;

import org.json.JSONException;
import org.json.JSONObject;

import java.nio.charset.StandardCharsets;

/** Wire format shared with the Windows server (see server/server.go). */
final class Protocol {
    static final int DEFAULT_PORT = 47800;
    static final int DISCOVERY_PORT = 47801;
    static final String DISCOVERY_QUERY = "RDESK?";
    static final int VERSION = 1;

    static final byte MSG_JSON = 'J';
    static final byte MSG_FRAME = 'F';
    static final byte MSG_ACK = 'A';

    // Windows virtual-key codes
    static final int VK_BACK = 0x08, VK_TAB = 0x09, VK_RETURN = 0x0D, VK_SHIFT = 0x10, VK_CONTROL = 0x11,
            VK_MENU = 0x12, VK_ESCAPE = 0x1B, VK_SPACE = 0x20, VK_PRIOR = 0x21, VK_NEXT = 0x22,
            VK_END = 0x23, VK_HOME = 0x24, VK_LEFT = 0x25, VK_UP = 0x26, VK_RIGHT = 0x27, VK_DOWN = 0x28,
            VK_SNAPSHOT = 0x2C, VK_INSERT = 0x2D, VK_DELETE = 0x2E, VK_LWIN = 0x5B, VK_APPS = 0x5D,
            VK_F1 = 0x70, VK_LSHIFT = 0xA0, VK_RSHIFT = 0xA1, VK_LCONTROL = 0xA2, VK_RCONTROL = 0xA3,
            VK_LMENU = 0xA4, VK_RMENU = 0xA5, VK_VOLUME_MUTE = 0xAD, VK_VOLUME_DOWN = 0xAE,
            VK_VOLUME_UP = 0xAF, VK_MEDIA_NEXT = 0xB0, VK_MEDIA_PREV = 0xB1, VK_MEDIA_PLAY_PAUSE = 0xB3;

    private Protocol() {}

    static byte[] packet(byte type, byte[] payload) {
        int n = payload == null ? 0 : payload.length;
        byte[] out = new byte[5 + n];
        out[0] = type;
        out[1] = (byte) (n >>> 24);
        out[2] = (byte) (n >>> 16);
        out[3] = (byte) (n >>> 8);
        out[4] = (byte) n;
        if (n > 0) System.arraycopy(payload, 0, out, 5, n);
        return out;
    }

    static byte[] json(JSONObject o) {
        return packet(MSG_JSON, o.toString().getBytes(StandardCharsets.UTF_8));
    }

    /** obj("t", "mr", "dx", 3) → {"t":"mr","dx":3} */
    static JSONObject obj(Object... kv) {
        JSONObject o = new JSONObject();
        try {
            for (int i = 0; i + 1 < kv.length; i += 2) o.put((String) kv[i], kv[i + 1]);
        } catch (JSONException ignored) {
        }
        return o;
    }

    static float readFloat(byte[] b, int off) {
        int bits = ((b[off] & 0xFF) << 24) | ((b[off + 1] & 0xFF) << 16) | ((b[off + 2] & 0xFF) << 8) | (b[off + 3] & 0xFF);
        return Float.intBitsToFloat(bits);
    }

    static boolean isModifier(int vk) {
        switch (vk) {
            case VK_SHIFT: case VK_CONTROL: case VK_MENU: case VK_LWIN:
            case VK_LSHIFT: case VK_RSHIFT: case VK_LCONTROL: case VK_RCONTROL: case VK_LMENU: case VK_RMENU:
                return true;
            default:
                return false;
        }
    }
}
