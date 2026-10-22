package com.rectcircle.fncpn;

import android.app.Activity;
import android.content.Context;
import android.content.Intent;
import android.net.VpnService;
import android.os.Handler;
import android.os.Looper;
import org.json.JSONObject;
import java.lang.ref.WeakReference;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

/** Fixed JNI entry point. Does not own business state or Fyne windows. */
public final class Bridge {
    static { System.loadLibrary("fncpn"); }
    static volatile FnVpnService service;
    static volatile Context application;
    static volatile String startupError;
    static volatile CountDownLatch ready = new CountDownLatch(1);
    private static WeakReference<MainActivity> activity = new WeakReference<>(null);
    private static final Handler main = new Handler(Looper.getMainLooper());
    static final java.util.concurrent.ExecutorService lifecycle = java.util.concurrent.Executors.newSingleThreadExecutor();
    private static CountDownLatch permission;
    private static boolean permitted;
    static native String start(FnVpnService owner, String directory);
    private static native void prepare();
    static native void stop(boolean revoked);
    static native void networkChanged();
    static native String command(String method, String params);

    static void attach(MainActivity owner) { prepare(); application=owner.getApplicationContext(); activity = new WeakReference<>(owner); }
    static void detach(MainActivity owner) { if (activity.get() == owner) activity.clear(); }

    private static synchronized void ensure() throws Exception {
        if (service != null && ready.getCount() == 0) {
            if (startupError != null) throw new Exception(startupError);
            return;
        }
        MainActivity owner = activity.get();
        if (owner == null) throw new Exception("Open FnCPN to authorize VPN access");
        permission = new CountDownLatch(1);
        permitted = false;
        main.post(() -> {
            Intent request = VpnService.prepare(owner);
            if (request == null) permissionResult(true);
            else owner.startActivityForResult(request, MainActivity.VPN_REQUEST);
        });
        if (!permission.await(60, TimeUnit.SECONDS) || !permitted) throw new Exception("VPN permission was not granted");
        ready = new CountDownLatch(1);
        startupError = null;
        main.post(() -> {
            try { owner.startForegroundService(new Intent(owner, FnVpnService.class)); }
            catch (RuntimeException error) { startupError = "Cannot start VPN service"; ready.countDown(); }
        });
        if (!ready.await(15, TimeUnit.SECONDS)) throw new Exception("VPN service startup timed out");
        if (startupError != null) throw new Exception(startupError);
    }
    static void permissionResult(boolean granted) {
        permitted = granted;
        CountDownLatch pending = permission;
        if (pending != null) pending.countDown();
    }

    // Called on an attached native worker, never the Android main thread.
    public static String rpc(String raw) {
        try {
            JSONObject request = new JSONObject(raw);
            if(request.getInt("version")!=1)throw new Exception("Android bridge version mismatch; complete the app update");
            String method = request.getString("method");
            JSONObject params = request.optJSONObject("params");
            if (params == null) params = new JSONObject();
            Object result;
            if (method.equals("service.ensure")) { ensure(); result = JSONObject.NULL; }
            else if(method.equals("settings.forget")) {
                Context context=application;if(context==null)throw new Exception("Open FnCPN first");
                new Credentials(context).dispatch("secret.reset",new JSONObject());
                java.io.File config=new java.io.File(context.getFilesDir(),"client.json");
                if(config.exists() && !config.delete())throw new Exception("Cannot remove local configuration");
                result=JSONObject.NULL;
            }
            else if (method.equals("ui.admin")) {
                MainActivity owner = activity.get();
                if (owner == null) throw new Exception("Open FnCPN first");
                String url = params.getString("url");
                main.post(() -> owner.startActivity(new Intent(owner, AdminActivity.class).putExtra("url", url)));
                result = JSONObject.NULL;
            } else if (method.equals("ui.share")) {
                MainActivity owner = activity.get();
                if (owner == null) throw new Exception("Open FnCPN first");
                String text = params.getString("text");
                if (text.length() > 262144) throw new Exception("Diagnostic report is too large");
                main.post(() -> owner.startActivity(Intent.createChooser(new Intent(Intent.ACTION_SEND)
                    .setType("text/plain").putExtra(Intent.EXTRA_TEXT, text), "FnCPN")));
                result = JSONObject.NULL;
            } else {
                FnVpnService owner = service;
                if (owner == null) throw new Exception("VPN service is not running");
                result = owner.dispatch(method, params);
            }
            return new JSONObject().put("version",1).put("ok", true).put("result", result).toString();
        } catch (Exception error) {
            // Never include request content, credentials or stack traces.
            try { return new JSONObject().put("version",1).put("ok", false).put("error", safeError(error)).toString(); }
            catch (Exception impossible) { return "{\"version\":1,\"ok\":false,\"error\":\"Android system operation failed\"}"; }
        }
    }
    private static String safeError(Exception error) {
        if (error instanceof java.security.GeneralSecurityException) return "Credential key is unavailable; reset local settings and sign in again";
        if (error instanceof org.json.JSONException) return "Invalid Android system request";
        if (error.getClass() == Exception.class) return error.getMessage();
        return "Android system operation failed: " + error.getClass().getSimpleName();
    }
    private Bridge() {}
}
