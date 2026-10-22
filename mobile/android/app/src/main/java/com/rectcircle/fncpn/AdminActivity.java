package com.rectcircle.fncpn;

import android.app.Activity;
import android.os.Bundle;
import android.net.Uri;
import android.webkit.CookieManager;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;

public final class AdminActivity extends Activity {
    private WebView web;
    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        String url = getIntent().getStringExtra("url");
        Uri origin = url == null ? null : Uri.parse(url);
        if (origin == null || !"http".equals(origin.getScheme()) || !"127.0.0.1".equals(origin.getHost()) || origin.getPort() <= 0 || origin.getUserInfo() != null) { finish(); return; }
        web = new WebView(this);
        web.getSettings().setJavaScriptEnabled(true);
        web.getSettings().setDomStorageEnabled(true);
        web.getSettings().setAllowFileAccess(false); web.getSettings().setAllowContentAccess(false);
        web.getSettings().setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
        CookieManager.getInstance().setAcceptThirdPartyCookies(web,false);
        web.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                Uri target = request.getUrl();
                return !"http".equals(target.getScheme()) || !"127.0.0.1".equals(target.getHost()) || target.getPort() != origin.getPort();
            }
        });
        setContentView(web); web.loadUrl(url);
    }
    @Override public void onDestroy() { if (web != null) { web.stopLoading(); web.destroy(); } super.onDestroy(); }
}
