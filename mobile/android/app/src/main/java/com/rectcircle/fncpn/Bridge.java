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
    static native void back();
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

    static void initialize(Context context){application=context.getApplicationContext();prepare();}
    static void attach(MainActivity owner) { prepare(); application=owner.getApplicationContext(); activity = new WeakReference<>(owner); SupportLog.event(application,"ui_attach",null); }
    static void detach(MainActivity owner) { if (activity.get() == owner) activity.clear(); }

    private static synchronized void ensure() throws Exception {
        if (service != null && ready.getCount() == 0) {
            if (startupError != null) throw new Exception(startupError);
            return;
        }
        Activity owner = activity.get();
        if (owner == null) throw new Exception("Open FnCPN to authorize VPN access");
        final Activity permissionOwner=owner;
        permission = new CountDownLatch(1);
        permitted = false;
        main.post(() -> {
            Intent request = VpnService.prepare(permissionOwner);
            if (request == null) permissionResult(true);
            else permissionOwner.startActivityForResult(request, MainActivity.VPN_REQUEST);
        });
        if (!permission.await(60, TimeUnit.SECONDS) || !permitted) throw new Exception("VPN permission was not granted");
        ready = new CountDownLatch(1);
        startupError = null;
        main.post(() -> {
            try { permissionOwner.startForegroundService(new Intent(permissionOwner, FnVpnService.class)); }
            catch (RuntimeException error) { SupportLog.event(application,"service_start_failed",error); startupError = "Cannot start VPN service"; ready.countDown(); }
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
        String operation="decode";
        try {
            JSONObject request = new JSONObject(raw);
            if(request.getInt("version")!=1)throw new Exception("Android bridge version mismatch; complete the app update");
            String method = request.getString("method");
            switch(method) {
                case "network.snapshot": case "network.resolve": case "network.dns": case "network.protect":
                case "vpn.establish": case "service.ensure": case "ui.export": case "support.read":
                    operation=method.replace('.','_');break;
                default:operation="other";
            }
            JSONObject params = request.optJSONObject("params");
            if (params == null) params = new JSONObject();
            Object result;
            if (method.equals("service.ensure")) { ensure(); result = JSONObject.NULL; }
            else if(method.equals("settings.status")) {
                Context context=application;if(context==null)throw new Exception("Open FnCPN first");
                java.io.File config=new java.io.File(context.getFilesDir(),"client.json");boolean automatic=false;
                if(config.isFile()){try{automatic=new JSONObject(new String(java.nio.file.Files.readAllBytes(config.toPath()),java.nio.charset.StandardCharsets.UTF_8)).optBoolean("autoConnect");}catch(Exception invalid){throw new Exception("Cannot read saved connection settings");}}
                result=new JSONObject().put("configured",config.isFile()).put("autoConnect",automatic);
            }
            else if(method.equals("settings.forget")) {
                Context context=application;if(context==null)throw new Exception("Open FnCPN first");
                new Credentials(context).dispatch("secret.reset",new JSONObject());
                java.io.File config=new java.io.File(context.getFilesDir(),"client.json");
                if(config.exists() && !config.delete())throw new Exception("Cannot remove local configuration");
                result=JSONObject.NULL;
            }
            else if (method.equals("ui.logs")) {
                MainActivity owner=activity.get();if(owner==null)throw new Exception("Open FnCPN first");
                android.net.Uri directory=android.provider.DocumentsContract.buildDocumentUri(owner.getPackageName()+".logs","logs");
                main.post(()->{
                    android.net.Uri root=android.provider.DocumentsContract.buildRootUri(owner.getPackageName()+".logs","logs");
                    Intent browse=new Intent(Intent.ACTION_VIEW).setDataAndType(root,"vnd.android.document/root").addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
                    java.util.List<android.content.pm.ResolveInfo> handlers=owner.getPackageManager().queryIntentActivities(browse,android.content.pm.PackageManager.MATCH_DEFAULT_ONLY);
                    for(android.content.pm.ResolveInfo handler:handlers){if((handler.activityInfo.applicationInfo.flags&android.content.pm.ApplicationInfo.FLAG_SYSTEM)!=0){browse.setComponent(new android.content.ComponentName(handler.activityInfo.packageName,handler.activityInfo.name));break;}}
                    try{owner.startActivity(browse);}catch(android.content.ActivityNotFoundException unavailable){
                        Intent picker=new Intent(Intent.ACTION_OPEN_DOCUMENT).setType("text/plain").addCategory(Intent.CATEGORY_OPENABLE).putExtra(android.provider.DocumentsContract.EXTRA_INITIAL_URI,directory);
                        try{owner.startActivity(picker);}catch(android.content.ActivityNotFoundException missing){android.widget.Toast.makeText(owner,"未找到系统文件管理器",android.widget.Toast.LENGTH_SHORT).show();}
                    }
                });result=JSONObject.NULL;
            } else if(method.equals("device.name")) {
                Context context=application;String name=android.provider.Settings.Global.getString(context.getContentResolver(),"device_name");
                if(name==null||name.trim().isEmpty())name=android.os.Build.MODEL;result=name;
            } else if(method.equals("ui.background")){MainActivity owner=activity.get();if(owner!=null)main.post(()->owner.moveTaskToBack(true));result=JSONObject.NULL;
            } else if(method.equals("ui.nas")){MainActivity owner=activity.get();if(owner==null)throw new Exception("Open FnCPN first");android.net.Uri uri=android.net.Uri.parse(params.getString("url"));if(!"http".equals(uri.getScheme())&& !"https".equals(uri.getScheme()))throw new Exception("Unsupported URL");main.post(()->owner.startActivity(new Intent(Intent.ACTION_VIEW,uri)));result=JSONObject.NULL;
            } else if(method.equals("ui.toast")){ MainActivity owner=activity.get();if(owner==null)throw new Exception("Open FnCPN first");String text=params.getString("text");main.post(()->android.widget.Toast.makeText(owner,text,android.widget.Toast.LENGTH_SHORT).show());result=JSONObject.NULL;
            } else if (method.equals("ui.browser")) {
                MainActivity owner=activity.get();if(owner==null)throw new Exception("Open FnCPN first");
                String url=params.getString("url");
                if(!"https://fncpn.rectcircle.cn/#donate".equals(url))throw new Exception("Unsupported website");
                main.post(()->owner.startActivity(new Intent(Intent.ACTION_VIEW,android.net.Uri.parse(url))));result=JSONObject.NULL;
            } else if (method.equals("ui.exit")) {
                MainActivity owner=activity.get();if(owner==null)throw new Exception("Open FnCPN first");
                main.post(()->{FnVpnService running=service;if(running!=null)running.stopSelf();owner.finishAndRemoveTask();});result=JSONObject.NULL;
            } else if (method.equals("ui.admin")) {
                MainActivity owner = activity.get();
                if (owner == null) throw new Exception("Open FnCPN first");
                String url = params.getString("url");
                main.post(() -> {owner.startActivity(new Intent(owner, AdminActivity.class).putExtra("url", url));AppChrome.enter(owner);});
                result = JSONObject.NULL;
            } else if (method.equals("support.read")) {
                Context context=application;if(context==null)throw new Exception("Open FnCPN first");
                JSONObject logs=new JSONObject();
                for(String name:new String[]{"client.log.2","client.log.1","client.log","android.log.1","android.log"}) {
                    java.io.File file=new java.io.File(context.getFilesDir(),"logs/"+name);
                    if(!file.isFile())continue;
                    try(java.io.RandomAccessFile input=new java.io.RandomAccessFile(file,"r")) {
                        long start=Math.max(0,input.length()-262144);input.seek(start);
                        if(start>0)input.readLine(); // omit a partial first record
                        byte[] data=new byte[(int)(input.length()-input.getFilePointer())];input.readFully(data);
                        logs.put(name,new String(data,java.nio.charset.StandardCharsets.UTF_8));
                    }
                }
                result=logs;
            } else if (method.equals("ui.export")) {
                MainActivity owner=activity.get();if(owner==null)throw new Exception("Open FnCPN first");
                String text=params.getString("text");if(text.length()>4194304)throw new Exception("Support report is too large");
                java.io.File directory=new java.io.File(owner.getCacheDir(),"support");directory.mkdirs();
                java.io.File[] old=directory.listFiles();
                if(old!=null) {
                    java.util.Arrays.sort(old,java.util.Comparator.comparingLong(java.io.File::lastModified).reversed());
                    for(int i=0;i<old.length;i++)if(i>=7 || System.currentTimeMillis()-old[i].lastModified()>86400000L)old[i].delete();
                }
                java.io.File file=new java.io.File(directory,"support-"+java.util.UUID.randomUUID()+".zip");
                try(java.util.zip.ZipOutputStream zip=new java.util.zip.ZipOutputStream(new java.io.FileOutputStream(file))) {
                    zip.putNextEntry(new java.util.zip.ZipEntry("support.json"));zip.write(text.getBytes(java.nio.charset.StandardCharsets.UTF_8));zip.closeEntry();
                }
                android.net.Uri uri=new android.net.Uri.Builder().scheme("content").authority(owner.getPackageName()+".support").appendPath(file.getName()).build();
                main.post(() -> {
                    Intent share=new Intent(Intent.ACTION_SEND).setType("application/zip").putExtra(Intent.EXTRA_STREAM,uri)
                        .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
                    share.setClipData(android.content.ClipData.newRawUri("FnCPN support",uri));
                    owner.startActivity(Intent.createChooser(share,"导出诊断与日志"));
                });
                SupportLog.event(application,"support_export",null);
                result=JSONObject.NULL;
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
            SupportLog.event(application,"system_operation_failed_"+operation,error);
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
