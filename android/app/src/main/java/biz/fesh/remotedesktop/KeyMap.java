package biz.fesh.remotedesktop;

import android.view.KeyEvent;

/** Android key codes → Windows virtual-key codes. */
final class KeyMap {
    private KeyMap() {}

    static int special(int keyCode) {
        switch (keyCode) {
            case KeyEvent.KEYCODE_DEL: return Protocol.VK_BACK;
            case KeyEvent.KEYCODE_FORWARD_DEL: return Protocol.VK_DELETE;
            case KeyEvent.KEYCODE_ENTER:
            case KeyEvent.KEYCODE_NUMPAD_ENTER: return Protocol.VK_RETURN;
            case KeyEvent.KEYCODE_TAB: return Protocol.VK_TAB;
            case KeyEvent.KEYCODE_ESCAPE: return Protocol.VK_ESCAPE;
            case KeyEvent.KEYCODE_DPAD_LEFT: return Protocol.VK_LEFT;
            case KeyEvent.KEYCODE_DPAD_RIGHT: return Protocol.VK_RIGHT;
            case KeyEvent.KEYCODE_DPAD_UP: return Protocol.VK_UP;
            case KeyEvent.KEYCODE_DPAD_DOWN: return Protocol.VK_DOWN;
            case KeyEvent.KEYCODE_MOVE_HOME: return Protocol.VK_HOME;
            case KeyEvent.KEYCODE_MOVE_END: return Protocol.VK_END;
            case KeyEvent.KEYCODE_PAGE_UP: return Protocol.VK_PRIOR;
            case KeyEvent.KEYCODE_PAGE_DOWN: return Protocol.VK_NEXT;
            case KeyEvent.KEYCODE_INSERT: return Protocol.VK_INSERT;
            case KeyEvent.KEYCODE_SYSRQ: return Protocol.VK_SNAPSHOT;
            case KeyEvent.KEYCODE_MENU: return Protocol.VK_APPS;
            case KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE: return Protocol.VK_MEDIA_PLAY_PAUSE;
            case KeyEvent.KEYCODE_MEDIA_NEXT: return Protocol.VK_MEDIA_NEXT;
            case KeyEvent.KEYCODE_MEDIA_PREVIOUS: return Protocol.VK_MEDIA_PREV;
            default:
                if (keyCode >= KeyEvent.KEYCODE_F1 && keyCode <= KeyEvent.KEYCODE_F12) {
                    return Protocol.VK_F1 + (keyCode - KeyEvent.KEYCODE_F1);
                }
                return 0;
        }
    }

    static int modifier(int keyCode) {
        switch (keyCode) {
            case KeyEvent.KEYCODE_SHIFT_LEFT: return Protocol.VK_LSHIFT;
            case KeyEvent.KEYCODE_SHIFT_RIGHT: return Protocol.VK_RSHIFT;
            case KeyEvent.KEYCODE_CTRL_LEFT: return Protocol.VK_LCONTROL;
            case KeyEvent.KEYCODE_CTRL_RIGHT: return Protocol.VK_RCONTROL;
            case KeyEvent.KEYCODE_ALT_LEFT: return Protocol.VK_LMENU;
            case KeyEvent.KEYCODE_ALT_RIGHT: return Protocol.VK_RMENU;
            case KeyEvent.KEYCODE_META_LEFT:
            case KeyEvent.KEYCODE_META_RIGHT: return Protocol.VK_LWIN;
            default: return 0;
        }
    }

    static int letterOrDigit(int keyCode) {
        if (keyCode >= KeyEvent.KEYCODE_A && keyCode <= KeyEvent.KEYCODE_Z) return 0x41 + (keyCode - KeyEvent.KEYCODE_A);
        if (keyCode >= KeyEvent.KEYCODE_0 && keyCode <= KeyEvent.KEYCODE_9) return 0x30 + (keyCode - KeyEvent.KEYCODE_0);
        if (keyCode == KeyEvent.KEYCODE_SPACE) return Protocol.VK_SPACE;
        return 0;
    }
}
