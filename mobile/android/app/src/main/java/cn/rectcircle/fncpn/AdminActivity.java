package cn.rectcircle.fncpn;

import android.app.Activity;
import android.os.Bundle;
import android.os.Build;
import android.net.Uri;
import android.webkit.*;
import android.widget.*;
import android.view.View;

/** One temporary web session, isolated from the main Fyne runtime. */
public final class AdminActivity extends Activity {
    private WebView web;
    private FrameLayout frame;
    private LinearLayout loading;
    private TextView message;
    private ProgressBar progress;
    private Button retry;
    private Uri origin;
    private String entry;
    private boolean closed, initialized;
    private final java.util.List<WebView> popups=new java.util.ArrayList<>();
    private static boolean storageConfigured;

    @Override public void onCreate(Bundle state) {
        AppChrome.theme(this);super.onCreate(state);
        entry=getIntent().getStringExtra("url");origin=entry==null?null:Uri.parse(entry);
        if(origin==null || !"http".equals(origin.getScheme()) || !"127.0.0.1".equals(origin.getHost()) || origin.getPort()<=0 || origin.getUserInfo()!=null){finish();return;}
        frame=new FrameLayout(this);
        loading=new LinearLayout(this);loading.setOrientation(LinearLayout.VERTICAL);loading.setGravity(android.view.Gravity.CENTER);loading.setBackgroundColor(AppChrome.background(this));
        progress=new ProgressBar(this);loading.addView(progress);
        message=new TextView(this);message.setTextColor(AppChrome.primary(this));message.setText(R.string.opening_admin);loading.addView(message);
        retry=new Button(this);retry.setText(R.string.retry);retry.setVisibility(View.GONE);retry.setOnClickListener(v->{showLoading();web.loadUrl(entry);});loading.addView(retry);
        frame.addView(loading,new FrameLayout.LayoutParams(-1,-1));AppChrome.install(this,frame);
        // Draw the shell before loading Chromium or clearing its data.
        frame.getViewTreeObserver().addOnDrawListener(new android.view.ViewTreeObserver.OnDrawListener(){
            @Override public void onDraw(){if(initialized)return;initialized=true;frame.post(()->{frame.getViewTreeObserver().removeOnDrawListener(this);if(!closed)initializeWeb();});}
        });
    }
    private void initializeWeb() {
        showLoading();
        try {
            if(!storageConfigured){if(Build.VERSION.SDK_INT>=28)WebView.setDataDirectorySuffix("admin");storageConfigured=true;}
            web=new WebView(this);
            WebSettings settings=web.getSettings();settings.setJavaScriptEnabled(true);settings.setDomStorageEnabled(true);
            settings.setAllowFileAccess(false);settings.setAllowContentAccess(false);settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);settings.setSupportMultipleWindows(true);settings.setJavaScriptCanOpenWindowsAutomatically(false);
            CookieManager.getInstance().setAcceptThirdPartyCookies(web,false);
            web.setWebChromeClient(new WebChromeClient(){
                @Override public boolean onCreateWindow(WebView view,boolean dialog,boolean gesture,android.os.Message result){
                    WebView popup=new WebView(AdminActivity.this);popups.add(popup);
                    popup.setWebViewClient(new WebViewClient(){
                        @Override public boolean shouldOverrideUrlLoading(WebView child,WebResourceRequest request){
                            navigate(request.getUrl(),gesture && !request.isRedirect(),true);
                            child.post(()->{if(popups.remove(child))child.destroy();});return true;
                        }
                    });
                    popup.postDelayed(()->{if(popups.remove(popup))popup.destroy();},10000);
                    ((WebView.WebViewTransport)result.obj).setWebView(popup);result.sendToTarget();return true;
                }
            });
            web.setWebViewClient(new WebViewClient(){
                @Override public boolean shouldOverrideUrlLoading(WebView view,WebResourceRequest request){
                    return request.isForMainFrame() && navigate(request.getUrl(),request.hasGesture() && !request.isRedirect(),false);
                }
                @Override public void onPageStarted(WebView view,String url,android.graphics.Bitmap icon){showLoading();}
                @Override public void onPageCommitVisible(WebView view,String url){if(!failed)loading.setVisibility(View.GONE);}
                @Override public void onPageFinished(WebView view,String url){if(!failed)loading.setVisibility(View.GONE);}
                @Override public void onReceivedError(WebView view,WebResourceRequest request,WebResourceError error){if(request.isForMainFrame())showFailure();}
                @Override public void onReceivedHttpError(WebView view,WebResourceRequest request,WebResourceResponse response){if(request.isForMainFrame())showFailure();}
            });
            frame.addView(web,0,new FrameLayout.LayoutParams(-1,-1));
            android.webkit.WebStorage.getInstance().deleteAllData();web.clearCache(true);
            CookieManager.getInstance().removeAllCookies(removed->{if(!closed && web!=null)web.loadUrl(entry);});
            SupportLog.event(this,"admin_open",null);
        } catch(RuntimeException error){SupportLog.event(this,"admin_open_failed",error);showFailure();retry.setOnClickListener(v->{if(web!=null){frame.removeView(web);web.destroy();web=null;}showLoading();frame.post(this::initializeWeb);});}
    }
    private boolean failed;
    private int loadGeneration;
    private void showLoading(){failed=false;int generation=++loadGeneration;loading.setVisibility(View.VISIBLE);progress.setVisibility(View.VISIBLE);message.setText(R.string.opening_admin);retry.setVisibility(View.GONE);loading.postDelayed(()->{if(!closed && generation==loadGeneration && loading.getVisibility()==View.VISIBLE){if(web!=null)web.stopLoading();showFailure();}},30000);}
    private void showFailure(){failed=true;loadGeneration++;loading.setVisibility(View.VISIBLE);progress.setVisibility(View.GONE);message.setText(R.string.admin_failed);retry.setVisibility(View.VISIBLE);}
    private boolean navigate(Uri target,boolean gesture,boolean popup){
        if(sameOrigin(target)){if(popup && web!=null)web.loadUrl(target.toString());return popup;}
        if(gesture && external(target))try{startActivity(new android.content.Intent(android.content.Intent.ACTION_VIEW,target));}catch(android.content.ActivityNotFoundException missing){message.setText(R.string.browser_missing);loading.setVisibility(View.VISIBLE);progress.setVisibility(View.GONE);retry.setVisibility(View.VISIBLE);}
        return true;
    }
    private boolean sameOrigin(Uri target){return target!=null && AdminNavigation.sameOrigin(entry,target.toString());}
    private boolean external(Uri target){return target!=null && AdminNavigation.external(target.toString());}
    @Override public void onBackPressed(){if(web!=null && web.canGoBack())web.goBack();else super.onBackPressed();}
    @Override public void finish(){super.finish();AppChrome.leave(this);}
    @Override public void onDestroy(){closed=true;loadGeneration++;for(WebView popup:popups)popup.destroy();popups.clear();if(web!=null){web.stopLoading();web.clearCache(true);WebStorage.getInstance().deleteAllData();CookieManager.getInstance().removeAllCookies(null);CookieManager.getInstance().flush();web.destroy();web=null;}super.onDestroy();}
}
