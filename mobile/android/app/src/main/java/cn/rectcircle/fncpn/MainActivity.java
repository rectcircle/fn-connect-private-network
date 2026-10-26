package cn.rectcircle.fncpn;

import android.app.Activity;
import android.content.Intent;
import android.os.Bundle;
import android.os.Build;
import android.Manifest;
import android.content.pm.PackageManager;
import org.golang.app.FnNativeActivity;

public final class MainActivity extends FnNativeActivity {
    static final int VPN_REQUEST = 103;
    @Override public void onCreate(Bundle state) {
        // Load the same native library used by Fyne before its NativeActivity.
        Bridge.attach(this);
        if((getApplicationInfo().flags & android.content.pm.ApplicationInfo.FLAG_DEBUGGABLE)!=0 && getIntent().getBooleanExtra("fncpn.uiProfile",false))Bridge.profileUI(getCacheDir().getAbsolutePath());
        super.onCreate(state);
        if(Build.VERSION.SDK_INT>=33) getOnBackInvokedDispatcher().registerOnBackInvokedCallback(android.window.OnBackInvokedDispatcher.PRIORITY_DEFAULT,this::onBackPressed);
        if(Build.VERSION.SDK_INT>=33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)!=PackageManager.PERMISSION_GRANTED)
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS},104);
    }
    @Override protected void onNewIntent(Intent intent){super.onNewIntent(intent);setIntent(intent);Bridge.attach(this);}
    @Override protected void onActivityResult(int request, int result, Intent data) {
        if (request == VPN_REQUEST) Bridge.permissionResult(result == Activity.RESULT_OK);
        else super.onActivityResult(request, result, data);
    }
    // Fyne owns the page stack. Keep Android's keyboard-first back behavior.
    @Override public void onBackPressed() {
        android.view.View decor=getWindow().getDecorView();
        boolean keyboard;
        if(Build.VERSION.SDK_INT>=30) {
            android.view.WindowInsets insets=decor.getRootWindowInsets();
            keyboard=insets!=null && insets.isVisible(android.view.WindowInsets.Type.ime());
        } else {
            android.graphics.Rect visible=new android.graphics.Rect();decor.getWindowVisibleDisplayFrame(visible);
            keyboard=decor.getRootView().getHeight()-visible.bottom>150*getResources().getDisplayMetrics().density;
        }
        if(keyboard) FnNativeActivity.hideKeyboard(); else Bridge.back();
    }
    @Override public void onDestroy() { Bridge.detach(this); super.onDestroy(); }
}
