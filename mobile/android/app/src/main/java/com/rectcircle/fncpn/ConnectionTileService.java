package com.rectcircle.fncpn;

import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.net.VpnService;
import android.os.Build;
import android.service.quicksettings.Tile;
import android.service.quicksettings.TileService;
import java.io.File;

/** The tile owns no connection state: it sends commands to the same Go host. */
public final class ConnectionTileService extends TileService {
    private static final android.os.Handler main=new android.os.Handler(android.os.Looper.getMainLooper());
    private static java.lang.ref.WeakReference<ConnectionTileService> listening=new java.lang.ref.WeakReference<>(null);
    static volatile boolean active;
    static volatile boolean busy;
    static void changed(Context context,String state){
        active="LOCAL".equals(state)||"DIRECT".equals(state)||"RELAY".equals(state)||"RECONNECTING".equals(state)||"PROBING".equals(state);
        busy=false;
        refresh();
    }
    static void refresh(){main.post(()->{ConnectionTileService visible=listening.get();if(visible!=null)visible.render();});}
    @Override public void onStartListening(){listening=new java.lang.ref.WeakReference<>(this);render();}
    @Override public void onStopListening(){
        // A force-stopped package cannot run a cleanup callback, and some
        // systems refuse to bind it again. Do not leave an active cached tile
        // behind while invisible; onStartListening restores the live state.
        Tile tile=getQsTile();if(tile!=null){tile.setState(Tile.STATE_INACTIVE);if(Build.VERSION.SDK_INT>=29)tile.setSubtitle("待更新");tile.updateTile();}
        if(listening.get()==this)listening.clear();super.onStopListening();
    }
    private void render(){
        Tile tile=getQsTile();if(tile==null)return;tile.setLabel("FnCPN");tile.setState(busy?Tile.STATE_UNAVAILABLE:(isActive()?Tile.STATE_ACTIVE:Tile.STATE_INACTIVE));
        if(Build.VERSION.SDK_INT>=29)tile.setSubtitle(busy?"正在切换…":isActive()?"已连接":"已断开");tile.updateTile();
    }
    private static boolean isActive(){return active&&Bridge.service!=null&&Bridge.service.isReady();}
    @Override public void onClick(){super.onClick();if(isLocked()){unlockAndRun(this::toggle);return;}toggle();}
    private void toggle(){
        if(busy)return;
        if(!new File(getFilesDir(),"client.json").isFile()||VpnService.prepare(this)!=null){openApp();return;}
        busy=true;render();
        try{startForegroundService(new Intent(this,FnVpnService.class).setAction(isActive()?"tile-disconnect":"tile-connect"));}
        catch(RuntimeException error){busy=false;SupportLog.event(this,"tile_start_failed",error);openApp();}
    }
    private void openApp(){Intent intent=new Intent(this,MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK|Intent.FLAG_ACTIVITY_CLEAR_TOP|Intent.FLAG_ACTIVITY_SINGLE_TOP);
        if(Build.VERSION.SDK_INT>=34)startActivityAndCollapse(PendingIntent.getActivity(this,2,intent,PendingIntent.FLAG_UPDATE_CURRENT|PendingIntent.FLAG_IMMUTABLE));else startActivityAndCollapse(intent);
    }
}
