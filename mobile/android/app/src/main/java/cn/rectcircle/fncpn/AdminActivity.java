package cn.rectcircle.fncpn;

import android.app.Activity;
import android.os.Bundle;
import android.os.Build;
import android.net.Uri;
import android.view.WindowInsets;
import android.widget.LinearLayout;
import android.widget.Button;
import android.webkit.CookieManager;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;

public final class AdminActivity extends Activity {
    private WebView web;
    @Override public void onCreate(Bundle state) {
        AppChrome.theme(this);
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
                if (!request.isForMainFrame()) return false;
                Uri target = request.getUrl();
                return !"http".equals(target.getScheme()) || !"127.0.0.1".equals(target.getHost()) || target.getPort() != origin.getPort();
            }
        });
        AppChrome.install(this, web);
        SupportLog.event(this, "admin_open", null);
        web.loadUrl(url);
    }
    @Override public void onBackPressed() { if (web != null && web.canGoBack()) web.goBack(); else super.onBackPressed(); }
    @Override public void finish(){super.finish();AppChrome.leave(this);}
    @Override public void onDestroy() { if (web != null) { web.stopLoading(); web.destroy(); } super.onDestroy(); }
}
