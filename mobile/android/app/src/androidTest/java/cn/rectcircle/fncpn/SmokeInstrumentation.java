package cn.rectcircle.fncpn;

import android.app.Activity;
import android.app.Instrumentation;
import android.content.Intent;
import android.graphics.Rect;
import android.os.Bundle;
import android.view.View;
import android.view.ViewGroup;
import android.webkit.WebView;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

/** Device-only regression: no NAS credentials or external test framework needed. */
public final class SmokeInstrumentation extends Instrumentation {
    private WebView find(View view) {
        if (view instanceof WebView) return (WebView)view;
        if (view instanceof ViewGroup) {
            ViewGroup group=(ViewGroup)view;
            for(int i=0;i<group.getChildCount();i++) { WebView found=find(group.getChildAt(i));if(found!=null)return found; }
        }
        return null;
    }
    @Override public void onCreate(Bundle args) { super.onCreate(args);start(); }
    @Override public void onStart() {
        Bundle result=new Bundle(); Activity activity=null;
        try (ServerSocket server=new ServerSocket(0,4,java.net.InetAddress.getByName("127.0.0.1"))) {
            server.setSoTimeout(15000);
            Thread http=new Thread(() -> {
                try(Socket socket=server.accept()) {
                    byte[] body="<!doctype html><meta name='viewport' content='width=device-width'><title>FnCPN inset test</title><header style='position:fixed;top:0'>WEB CONTENT TOP</header><input style='margin-top:80px'>".getBytes(StandardCharsets.UTF_8);
                    socket.getOutputStream().write(("HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nContent-Length: "+body.length+"\r\nConnection: close\r\n\r\n").getBytes(StandardCharsets.UTF_8));
                    socket.getOutputStream().write(body);socket.getOutputStream().flush();
                } catch(Exception ignored) {}
            });http.start();
            Intent intent=new Intent().setClassName(getTargetContext().getPackageName(),"cn.rectcircle.fncpn.AdminActivity")
                .putExtra("url","http://127.0.0.1:"+server.getLocalPort()+"/").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            activity=startActivitySync(intent);
            waitForIdleSync();
            Activity owner=activity;
            String[] title={null};Rect bounds=new Rect();int[] safeTop={0};
            long deadline=System.currentTimeMillis()+10000;
            while(System.currentTimeMillis()<deadline) {
                CountDownLatch done=new CountDownLatch(1);
                runOnMainSync(() -> {
                    WebView web=find(owner.getWindow().getDecorView());
                    if(web==null)throw new AssertionError("Missing WebView");
                    web.getGlobalVisibleRect(bounds);
                    safeTop[0]=owner.getWindow().getDecorView().getRootWindowInsets().getSystemWindowInsetTop();
                    web.evaluateJavascript("document.title",value->{title[0]=value;done.countDown();});
                });
                done.await(1,TimeUnit.SECONDS);
                if("\"FnCPN inset test\"".equals(title[0]))break;
                Thread.sleep(100);
            }
            if(!"\"FnCPN inset test\"".equals(title[0]))throw new AssertionError("Web page failed to load: "+title[0]);
            int toolbar=Math.round(56*getTargetContext().getResources().getDisplayMetrics().density);
            if(bounds.top<safeTop[0]+toolbar || bounds.height()<100)throw new AssertionError("WebView overlaps toolbar/system bars: "+bounds+" inset="+safeTop[0]);
            java.io.File supportDirectory=new java.io.File(getTargetContext().getCacheDir(),"support");supportDirectory.mkdirs();
            java.io.File supportFile=new java.io.File(supportDirectory,"support-"+java.util.UUID.randomUUID()+".zip");
            try(java.io.FileOutputStream out=new java.io.FileOutputStream(supportFile)) {out.write(new byte[]{80,75,3,4});}
            android.net.Uri uri=new android.net.Uri.Builder().scheme("content").authority(getTargetContext().getPackageName()+".support").appendPath(supportFile.getName()).build();
            try(android.os.ParcelFileDescriptor file=getTargetContext().getContentResolver().openFileDescriptor(uri,"r")) {
                if(file==null || file.getStatSize()!=4)throw new AssertionError("Cannot read shared report");
            }
            boolean rejected=false;
            try(android.os.ParcelFileDescriptor ignored=getTargetContext().getContentResolver().openFileDescriptor(uri,"w")) {}
            catch(java.io.FileNotFoundException expected) {rejected=true;}
            if(!rejected)throw new AssertionError("Report provider accepted write access");
            supportFile.delete();
            android.graphics.Bitmap screenshot=getUiAutomation().takeScreenshot();
            if(screenshot!=null)try(java.io.FileOutputStream out=new java.io.FileOutputStream(new java.io.File(getTargetContext().getCacheDir(),"smoke-admin.png"))) {
                screenshot.compress(android.graphics.Bitmap.CompressFormat.PNG,100,out);screenshot.recycle();
            }
            result.putString("stream","PASS: report provider is read-only; real WebView loaded; top="+bounds.top+", systemInset="+safeTop[0]+", toolbar="+toolbar+"\n");
            finish(Activity.RESULT_OK,result);
        } catch(Throwable error) {
            result.putString("stream","FAIL: "+error+"\n");finish(Activity.RESULT_CANCELED,result);
        } finally { if(activity!=null){Activity owner=activity;runOnMainSync(owner::finish);} }
    }
}
