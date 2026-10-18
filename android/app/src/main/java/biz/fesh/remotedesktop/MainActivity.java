package biz.fesh.remotedesktop;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.graphics.Insets;
import android.os.Build;
import android.os.Bundle;
import android.text.InputType;
import android.view.LayoutInflater;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.view.inputmethod.EditorInfo;
import android.widget.BaseAdapter;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.ListView;
import android.widget.ProgressBar;
import android.widget.TextView;
import android.widget.Toast;

import java.util.ArrayList;
import java.util.List;

public class MainActivity extends Activity {

    private static final int REQ_REMOTE = 1;

    private Prefs prefs;
    private Discovery discovery;
    private final List<Item> items = new ArrayList<>();
    private Adapter adapter;
    private ProgressBar progress;
    private TextView empty;
    private ListView list;
    private EditText editHost;
    private Prefs.Host lastHost;

    private static final class Item {
        Prefs.Host host;
        boolean online;

        Item(Prefs.Host host, boolean online) {
            this.host = host;
            this.online = online;
        }
    }

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_main);
        prefs = new Prefs(this);

        View root = findViewById(R.id.root);
        applyInsets(root);

        progress = findViewById(R.id.progress);
        empty = findViewById(R.id.empty);
        list = findViewById(R.id.list);
        editHost = findViewById(R.id.editHost);
        adapter = new Adapter();
        list.setAdapter(adapter);

        list.setOnItemClickListener((p, v, pos, id) -> connect(items.get(pos).host, false, null));
        list.setOnItemLongClickListener((p, v, pos, id) -> {
            showHostMenu(items.get(pos).host);
            return true;
        });
        findViewById(R.id.btnSearch).setOnClickListener(v -> search());
        findViewById(R.id.btnConnect).setOnClickListener(v -> connectManual());
        editHost.setOnEditorActionListener((v, actionId, e) -> {
            if (actionId == EditorInfo.IME_ACTION_GO || actionId == EditorInfo.IME_ACTION_DONE) {
                connectManual();
                return true;
            }
            return false;
        });
    }

    private void applyInsets(View root) {
        if (Build.VERSION.SDK_INT < 30) return;
        getWindow().setDecorFitsSystemWindows(false);
        final int l = root.getPaddingLeft(), t = root.getPaddingTop(), r = root.getPaddingRight(), b = root.getPaddingBottom();
        root.setOnApplyWindowInsetsListener((v, insets) -> {
            Insets sys = insets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout());
            Insets ime = insets.getInsets(WindowInsets.Type.ime());
            v.setPadding(l + sys.left, t + sys.top, r + sys.right, b + Math.max(sys.bottom, ime.bottom));
            return WindowInsets.CONSUMED;
        });
    }

    @Override
    protected void onResume() {
        super.onResume();
        editHost.clearFocus();
        rebuildFromRecent();
        search();
    }

    @Override
    protected void onPause() {
        super.onPause();
        if (discovery != null) discovery.cancel();
        discovery = null;
        progress.setVisibility(View.GONE);
    }

    private void rebuildFromRecent() {
        List<Item> keepOnline = new ArrayList<>();
        for (Item it : items) if (it.online) keepOnline.add(it);
        items.clear();
        items.addAll(keepOnline);
        for (Prefs.Host h : prefs.recent()) {
            if (find(h.key()) == null) items.add(new Item(h, false));
        }
        adapter.notifyDataSetChanged();
        updateEmpty();
    }

    private Item find(String key) {
        for (Item it : items) if (it.host.key().equals(key)) return it;
        return null;
    }

    private void search() {
        if (discovery != null) return;
        for (Item it : items) it.online = false;
        adapter.notifyDataSetChanged();
        progress.setVisibility(View.VISIBLE);
        empty.setText("Шукаю комп'ютери в мережі…");
        updateEmpty();
        final Discovery d = new Discovery();
        discovery = d;
        d.search(new Discovery.Callback() {
            @Override
            public void onFound(String name, String host, int port) {
                if (discovery != d) return;
                Prefs.Host h = new Prefs.Host(name, host, port);
                Item it = find(h.key());
                if (it == null) {
                    items.add(0, new Item(h, true));
                } else {
                    it.host = h;
                    it.online = true;
                }
                adapter.notifyDataSetChanged();
                updateEmpty();
            }

            @Override
            public void onDone() {
                if (discovery != d) return;
                discovery = null;
                progress.setVisibility(View.GONE);
                empty.setText("Комп'ютерів не знайдено.\n\nЗапустіть RemoteDesktop.exe на ПК і перевірте, що телефон підключено до тієї ж Wi-Fi мережі. Або введіть IP-адресу з вікна сервера нижче.");
                updateEmpty();
            }
        });
    }

    private void updateEmpty() {
        boolean none = items.isEmpty();
        empty.setVisibility(none ? View.VISIBLE : View.GONE);
        list.setVisibility(none ? View.GONE : View.VISIBLE);
    }

    private void connectManual() {
        String s = editHost.getText().toString().trim();
        if (s.isEmpty()) {
            Toast.makeText(this, "Введіть IP-адресу комп'ютера", Toast.LENGTH_SHORT).show();
            return;
        }
        String host = s;
        int port = Protocol.DEFAULT_PORT;
        int colon = s.lastIndexOf(':');
        if (colon > 0 && s.indexOf(':') == colon) {
            host = s.substring(0, colon);
            try {
                port = Integer.parseInt(s.substring(colon + 1));
            } catch (NumberFormatException e) {
                Toast.makeText(this, "Невірний порт", Toast.LENGTH_SHORT).show();
                return;
            }
        }
        Item it = find(host + ":" + port);
        connect(it != null ? it.host : new Prefs.Host(host, host, port), false, null);
    }

    private void connect(Prefs.Host h, boolean askPin, String error) {
        String pin = prefs.pin(h.host, h.port);
        if (pin == null || askPin) showPinDialog(h, error);
        else startRemote(h, pin);
    }

    private void showPinDialog(Prefs.Host h, String error) {
        FrameLayout box = new FrameLayout(this);
        int pad = (int) (22 * getResources().getDisplayMetrics().density);
        box.setPadding(pad, pad / 2, pad, 0);
        EditText edit = new EditText(this);
        edit.setInputType(InputType.TYPE_CLASS_NUMBER | InputType.TYPE_NUMBER_VARIATION_PASSWORD);
        edit.setHint("PIN-код");
        edit.setSingleLine(true);
        String saved = prefs.pin(h.host, h.port);
        if (saved != null) edit.setText(saved);
        box.addView(edit);

        String msg = "Введіть PIN-код, показаний у вікні сервера на комп'ютері.";
        if (error != null) msg = error + "\n\n" + msg;
        AlertDialog dlg = new AlertDialog.Builder(this, R.style.Dialog)
                .setTitle(h.name)
                .setMessage(msg)
                .setView(box)
                .setPositiveButton("Підключити", (d, w) -> {
                    String pin = edit.getText().toString().trim();
                    if (!pin.isEmpty()) startRemote(h, pin);
                })
                .setNegativeButton("Скасувати", null)
                .create();
        dlg.setOnShowListener(d -> {
            edit.requestFocus();
            if (dlg.getWindow() != null) {
                dlg.getWindow().setSoftInputMode(android.view.WindowManager.LayoutParams.SOFT_INPUT_STATE_ALWAYS_VISIBLE);
            }
        });
        edit.setOnEditorActionListener((v, actionId, e) -> {
            String pin = edit.getText().toString().trim();
            if (!pin.isEmpty()) {
                dlg.dismiss();
                startRemote(h, pin);
            }
            return true;
        });
        dlg.show();
    }

    private void showHostMenu(Prefs.Host h) {
        new AlertDialog.Builder(this, R.style.Dialog)
                .setTitle(h.name)
                .setItems(new String[]{"Підключитися", "Змінити PIN-код", "Видалити зі списку"}, (d, which) -> {
                    if (which == 0) connect(h, false, null);
                    else if (which == 1) connect(h, true, null);
                    else {
                        prefs.removeRecent(h);
                        Item it = find(h.key());
                        if (it != null && !it.online) items.remove(it);
                        adapter.notifyDataSetChanged();
                        updateEmpty();
                    }
                })
                .show();
    }

    private void startRemote(Prefs.Host h, String pin) {
        lastHost = h;
        Intent i = new Intent(this, RemoteActivity.class);
        i.putExtra(RemoteActivity.EXTRA_HOST, h.host);
        i.putExtra(RemoteActivity.EXTRA_PORT, h.port);
        i.putExtra(RemoteActivity.EXTRA_NAME, h.name);
        i.putExtra(RemoteActivity.EXTRA_PIN, pin);
        startActivityForResult(i, REQ_REMOTE);
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == REQ_REMOTE && resultCode == RemoteActivity.RESULT_AUTH_FAILED && lastHost != null) {
            prefs.setPin(lastHost.host, lastHost.port, null);
            String msg = data != null ? data.getStringExtra(RemoteActivity.EXTRA_ERROR) : null;
            showPinDialog(lastHost, msg != null ? msg : "Невірний PIN-код");
        }
    }

    @Override
    protected void onSaveInstanceState(Bundle out) {
        super.onSaveInstanceState(out);
        if (lastHost != null) {
            out.putString("lh_name", lastHost.name);
            out.putString("lh_host", lastHost.host);
            out.putInt("lh_port", lastHost.port);
        }
    }

    @Override
    protected void onRestoreInstanceState(Bundle in) {
        super.onRestoreInstanceState(in);
        if (in.getString("lh_host") != null) {
            lastHost = new Prefs.Host(in.getString("lh_name"), in.getString("lh_host"), in.getInt("lh_port"));
        }
    }

    private final class Adapter extends BaseAdapter {
        @Override
        public int getCount() {
            return items.size();
        }

        @Override
        public Object getItem(int position) {
            return items.get(position);
        }

        @Override
        public long getItemId(int position) {
            return position;
        }

        @Override
        public View getView(int position, View convertView, ViewGroup parent) {
            View v = convertView != null ? convertView
                    : LayoutInflater.from(MainActivity.this).inflate(R.layout.item_host, parent, false);
            Item it = items.get(position);
            ((TextView) v.findViewById(R.id.name)).setText(it.host.name);
            String addr = it.host.host + (it.host.port != Protocol.DEFAULT_PORT ? ":" + it.host.port : "");
            ((TextView) v.findViewById(R.id.addr)).setText(addr);
            TextView st = v.findViewById(R.id.status);
            st.setText(it.online ? "● у мережі" : "");
            v.setAlpha(it.online ? 1f : 0.75f);
            return v;
        }
    }
}
