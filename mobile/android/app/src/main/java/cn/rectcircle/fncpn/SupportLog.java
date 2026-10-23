package cn.rectcircle.fncpn;

import android.content.Context;
import android.util.Log;
import org.json.JSONObject;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;

/** System events contain no RPC arguments, addresses, credentials or exception messages. */
final class SupportLog {
    static synchronized void event(Context context, String event, Throwable error) {
        try {
            JSONObject row = new JSONObject().put("time", java.time.Instant.now().toString())
                .put("platform", "android").put("event", event);
            if (error != null) row.put("errorType", error.getClass().getSimpleName());
            String line = row.toString();
            Log.i("FnCPN", line);
            if (context == null) return;
            File directory = new File(context.getFilesDir(), "logs");
            directory.mkdirs();
            File file = new File(directory, "android.log");
            if (file.length() > 512 * 1024) {
                File backup = new File(directory, "android.log.1");
                if (backup.exists() && !backup.delete()) return;
                if (!file.renameTo(backup)) return;
            }
            try (FileOutputStream out = new FileOutputStream(file, true)) {
                out.write((line + "\n").getBytes(StandardCharsets.UTF_8));
            }
        } catch (Exception ignored) { Log.w("FnCPN", "Cannot persist system event"); }
    }
    private SupportLog() {}
}
