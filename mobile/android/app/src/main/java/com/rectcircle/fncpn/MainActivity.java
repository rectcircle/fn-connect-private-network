package com.rectcircle.fncpn;

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
        super.onCreate(state);
        if(Build.VERSION.SDK_INT>=33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)!=PackageManager.PERMISSION_GRANTED)
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS},104);
    }
    @Override protected void onActivityResult(int request, int result, Intent data) {
        if (request == VPN_REQUEST) Bridge.permissionResult(result == Activity.RESULT_OK);
        else super.onActivityResult(request, result, data);
    }
    @Override public void onDestroy() { Bridge.detach(this); super.onDestroy(); }
}
