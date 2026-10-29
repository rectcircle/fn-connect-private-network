package cn.rectcircle.fncpn;

import android.app.Activity;
import android.content.res.Configuration;
import android.content.res.ColorStateList;
import android.graphics.Typeface;
import android.graphics.drawable.ColorDrawable;
import android.graphics.drawable.RippleDrawable;
import android.os.Build;
import android.view.Gravity;
import android.view.View;
import android.view.WindowInsets;
import android.view.WindowInsetsController;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

/** Flat 56dp chrome, with one attached hairline and no widget shadows. */
final class AppChrome {
    static int dp(Activity owner,float value){return Math.round(value*owner.getResources().getDisplayMetrics().density);}
    static boolean dark(Activity owner){return (owner.getResources().getConfiguration().uiMode&Configuration.UI_MODE_NIGHT_MASK)==Configuration.UI_MODE_NIGHT_YES;}
    static int primary(Activity owner){return dark(owner)?0xfff3f4f6:0xff171b21;}
    static int secondary(Activity owner){return dark(owner)?0xffa4acb8:0xff727b87;}
    static int background(Activity owner){return dark(owner)?0xff171b21:0xfff7f8fa;}
    static int surface(Activity owner){return dark(owner)?0xff202630:0xffffffff;}
    static RippleDrawable ripple(Activity owner){return new RippleDrawable(ColorStateList.valueOf(dark(owner)?0x24ffffff:0x14000000),null,new ColorDrawable(0xffffffff));}
    static void theme(Activity owner){owner.setTheme(dark(owner)?android.R.style.Theme_Material_NoActionBar:android.R.style.Theme_Material_Light_NoActionBar);}
    static LinearLayout header(Activity owner){
        LinearLayout bar=new LinearLayout(owner);bar.setGravity(Gravity.CENTER_VERTICAL);bar.setBackgroundColor(background(owner));bar.setElevation(0);
        FrameLayout back=new FrameLayout(owner);back.setBackground(ripple(owner));back.setContentDescription(owner.getString(R.string.back));back.setFocusable(true);back.setOnClickListener(v->owner.onBackPressed());
        back.addView(new ChromeIcon(owner,primary(owner)),new FrameLayout.LayoutParams(dp(owner,24),dp(owner,24),Gravity.CENTER));
        bar.addView(back,new LinearLayout.LayoutParams(dp(owner,48),dp(owner,56)));
        ImageView icon=new ImageView(owner);icon.setImageResource(R.drawable.app_icon);icon.setScaleType(ImageView.ScaleType.FIT_CENTER);
        LinearLayout.LayoutParams imageParams=new LinearLayout.LayoutParams(dp(owner,28),dp(owner,28));imageParams.leftMargin=dp(owner,8);imageParams.rightMargin=dp(owner,10);bar.addView(icon,imageParams);
        TextView title=new TextView(owner);title.setText("FnCPN");title.setTextSize(18);title.setTypeface(Typeface.create("sans-serif-medium",Typeface.NORMAL));title.setTextColor(primary(owner));bar.addView(title);
        TextView version=new TextView(owner);version.setTextSize(13);version.setTextColor(secondary(owner));
        try{version.setText(owner.getPackageManager().getPackageInfo(owner.getPackageName(),0).versionName);}catch(Exception ignored){}
        LinearLayout.LayoutParams versionParams=new LinearLayout.LayoutParams(-2,-2);versionParams.leftMargin=dp(owner,10);bar.addView(version,versionParams);return bar;
    }
    static void install(Activity owner,View content){
        LinearLayout root=new LinearLayout(owner);root.setOrientation(LinearLayout.VERTICAL);root.setBackgroundColor(background(owner));
        if(Build.VERSION.SDK_INT>=30)owner.getWindow().setDecorFitsSystemWindows(false);
        root.setOnApplyWindowInsetsListener((view,insets)->{
            if(Build.VERSION.SDK_INT>=30){android.graphics.Insets safe=insets.getInsets(WindowInsets.Type.systemBars()|WindowInsets.Type.displayCutout()|WindowInsets.Type.ime());view.setPadding(safe.left,safe.top,safe.right,safe.bottom);}
            else view.setPadding(insets.getSystemWindowInsetLeft(),insets.getSystemWindowInsetTop(),insets.getSystemWindowInsetRight(),insets.getSystemWindowInsetBottom());
            return insets;
        });
        root.addView(header(owner),new LinearLayout.LayoutParams(-1,dp(owner,56)));
        View divider=new View(owner);divider.setBackgroundColor(dark(owner)?0xff343c47:0xffe2e6eb);root.addView(divider,new LinearLayout.LayoutParams(-1,1));
        root.addView(content,new LinearLayout.LayoutParams(-1,0,1));owner.setContentView(root);root.requestApplyInsets();
        if(Build.VERSION.SDK_INT>=30){WindowInsetsController controller=owner.getWindow().getInsetsController();if(controller!=null){int flags=WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS|WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS;controller.setSystemBarsAppearance(dark(owner)?0:flags,flags);}}
        else owner.getWindow().getDecorView().setSystemUiVisibility(dark(owner)?0:View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR|View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR);
    }
    static void enter(Activity owner){owner.overridePendingTransition(R.anim.page_enter,R.anim.page_hold);}
    static void leave(Activity owner){owner.overridePendingTransition(R.anim.page_hold,R.anim.page_exit);}
    private AppChrome(){}
}
