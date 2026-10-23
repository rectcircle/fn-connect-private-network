package com.rectcircle.fncpn;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.net.ConnectivityManager;
import android.net.LinkAddress;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.NetworkRequest;
import android.net.VpnService;
import android.os.Build;
import android.os.ParcelFileDescriptor;
import android.system.OsConstants;
import org.json.JSONArray;
import org.json.JSONObject;
import java.net.InetAddress;
import java.net.NetworkInterface;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public final class FnVpnService extends VpnService {
    private final ExecutorService worker = Bridge.lifecycle;
    private ConnectivityManager connectivity;
    private Credentials credentials;
    private volatile Network physical;
    private volatile boolean revoked;
    private volatile boolean stopping;
    boolean isReady(){return !stopping&&Bridge.service==this&&Bridge.ready.getCount()==0;}
    private final ConnectivityManager.NetworkCallback callback = new ConnectivityManager.NetworkCallback() {
        @Override public void onAvailable(Network network) { changed(); }
        @Override public void onLost(Network network) { changed(); }
        @Override public void onCapabilitiesChanged(Network network, NetworkCapabilities capabilities) { changed(); }
        @Override public void onLinkPropertiesChanged(Network network, LinkProperties properties) { changed(); }
    };
    @Override public void onCreate() {
        super.onCreate();
        Bridge.service = this;
        Bridge.application=getApplicationContext();
        Bridge.initialize(this);
        credentials = new Credentials(this);
        connectivity = (ConnectivityManager) getSystemService(CONNECTIVITY_SERVICE);
        NotificationManager manager = (NotificationManager)getSystemService(NOTIFICATION_SERVICE);
        manager.createNotificationChannel(new NotificationChannel("connection", "FnCPN connection", NotificationManager.IMPORTANCE_LOW));
        connectivity.registerNetworkCallback(new NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN).build(), callback);
        chooseNetwork();
        SupportLog.event(this,"service_create",null);
    }
    private Notification notification(String text) {
        PendingIntent open = PendingIntent.getActivity(this,0,new Intent(this,MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP|Intent.FLAG_ACTIVITY_SINGLE_TOP),PendingIntent.FLAG_IMMUTABLE|PendingIntent.FLAG_UPDATE_CURRENT);
        PendingIntent pause = PendingIntent.getService(this,1,new Intent(this,FnVpnService.class).setAction("pause"),PendingIntent.FLAG_IMMUTABLE|PendingIntent.FLAG_UPDATE_CURRENT);
        return new Notification.Builder(this,"connection").setSmallIcon(R.drawable.tile_icon)
            .setContentTitle("FnCPN").setContentText(text).setContentIntent(open).setOngoing(true)
            .addAction(new Notification.Action.Builder(null,"Pause",pause).build()).build();
    }
    private boolean started;
    @Override public int onStartCommand(Intent intent, int flags, int startId) {
        try {
            if (Build.VERSION.SDK_INT >= 34) startForeground(1,notification("Starting connection service"),ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED);
            else startForeground(1,notification("Starting connection service"));
        } catch (RuntimeException error) {
            SupportLog.event(this,"foreground_service_failed",error);
            Bridge.startupError = "Android refused the VPN foreground service"; Bridge.ready.countDown(); stopSelf(); return START_NOT_STICKY;
        }
        if (!started) {
            started = true;
            worker.execute(() -> {
                String failure = Bridge.start(this,getFilesDir().getAbsolutePath());
                Bridge.startupError = failure.isEmpty() ? null : failure;
                SupportLog.event(this,failure.isEmpty()?"host_started":"host_start_failed",null);
                Bridge.ready.countDown();
                if (!failure.isEmpty()) stopSelf();
            });
        }
        if (intent != null && "pause".equals(intent.getAction())) worker.execute(() -> Bridge.command("disconnect","{}"));
        if(intent!=null && ("tile-connect".equals(intent.getAction())||"tile-disconnect".equals(intent.getAction())))worker.execute(()->{
            try{
                if(Bridge.startupError!=null)throw new Exception("Service startup failed");
                boolean connect="tile-connect".equals(intent.getAction());
                JSONObject command=new JSONObject(Bridge.command(connect?"connect":"disconnect","{}"));if(!command.optBoolean("ok"))throw new Exception("Connection command failed");
            }catch(Exception error){SupportLog.event(this,"tile_command_failed",error);new android.os.Handler(android.os.Looper.getMainLooper()).post(()->android.widget.Toast.makeText(this,"切换失败，请打开 FnCPN 查看诊断",android.widget.Toast.LENGTH_SHORT).show());}
            finally{ConnectionTileService.busy=false;ConnectionTileService.refresh();}
        });
        return START_STICKY;
    }
    private synchronized void chooseNetwork() {
        Network selected = null;
        Network active = connectivity.getActiveNetwork();
        for (Network network : connectivity.getAllNetworks()) {
            NetworkCapabilities c = connectivity.getNetworkCapabilities(network);
            if (c == null || !c.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) || !c.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) continue;
            if (network.equals(active)) { selected=network;break; }
            if (selected == null) selected=network;
            else {
                NetworkCapabilities previous=connectivity.getNetworkCapabilities(selected);
                if (previous != null && c.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) && !previous.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) selected=network;
            }
        }
        physical = selected;
        setUnderlyingNetworks(selected == null ? new Network[]{} : new Network[]{selected});
    }
    private void changed() { if (!stopping) { chooseNetwork(); SupportLog.event(this,"physical_network_changed",null); Bridge.networkChanged(); } }
    private Network network() throws Exception {
        Network result=physical; if(result==null)throw new Exception("No physical network is available"); return result;
    }
    private LinkProperties properties() throws Exception {
        LinkProperties p=connectivity.getLinkProperties(network());if(p==null)throw new Exception("Physical network properties are unavailable");return p;
    }
    Object dispatch(String method,JSONObject input) throws Exception {
        switch(method) {
        case "network.snapshot": {
            Network n=network();LinkProperties p=properties();String name=p.getInterfaceName();
            NetworkInterface iface=name==null?null:NetworkInterface.getByName(name);
            JSONArray prefixes=new JSONArray(),addresses=new JSONArray();
            for(LinkAddress address:p.getLinkAddresses()) {
                String ip=address.getAddress().getHostAddress().split("%")[0];
                addresses.put(ip);prefixes.put(ip+"/"+address.getPrefixLength());
            }
            // Some Android builds hide NetworkInterface metadata. Go's Android
            // socket adapter uses Network.bindSocket, not this display index.
            return new JSONObject().put("name",name==null?"android-physical":name).put("index",iface==null?1:iface.getIndex())
                .put("identity",n.toString()).put("prefixes",prefixes).put("addresses",addresses);
        }
        case "network.resolve": {
            JSONArray result=new JSONArray();for(InetAddress ip:network().getAllByName(input.getString("host")))result.put(ip.getHostAddress().split("%")[0]);return result;
        }
        case "network.dns": {
            LinkProperties p=properties();JSONArray servers=new JSONArray();for(InetAddress ip:p.getDnsServers())servers.put(ip.getHostAddress());
            String tlsName=Build.VERSION.SDK_INT>=28?p.getPrivateDnsServerName():null;
            return new JSONObject().put("servers",servers).put("tlsName",tlsName==null?"":tlsName);
        }
        case "network.protect": {
            int fd=input.getInt("fd");if(fd<0 || !protect(fd))throw new Exception("Cannot protect VPN transport socket");
            try(ParcelFileDescriptor duplicate=ParcelFileDescriptor.fromFd(fd)){network().bindSocket(duplicate.getFileDescriptor());}
            return JSONObject.NULL;
        }
        case "vpn.establish": {
            if (revoked || stopping) throw new Exception("VPN authorization was revoked");
            String[] address=input.getString("address").split("/");
            Builder b=new Builder().setSession("FnCPN").setMtu(input.getInt("mtu"))
                .addAddress(address[0],Integer.parseInt(address[1])).allowFamily(OsConstants.AF_INET6).setBlocking(false);
            JSONArray routes=input.getJSONArray("routes");
            if(routes.length()>65)throw new Exception("Too many VPN routes");
            for(int i=0;i<routes.length();i++){String[] route=routes.getString(i).split("/");int prefix=Integer.parseInt(route[1]);if(prefix==0)throw new Exception("Default VPN route is not allowed");b.addRoute(route[0],prefix);}
            String dns=input.optString("dns");if(!dns.isEmpty())b.addDnsServer(dns);
            b.setUnderlyingNetworks(new Network[]{network()});
            b.setConfigureIntent(PendingIntent.getActivity(this,0,new Intent(this,MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP|Intent.FLAG_ACTIVITY_SINGLE_TOP),PendingIntent.FLAG_IMMUTABLE));
            ParcelFileDescriptor descriptor=b.establish();if(descriptor==null)throw new Exception("VPN permission is required");
            // Go exclusively owns this fd after detach; Java must not close it.
            SupportLog.event(this,"vpn_established",null);
            return descriptor.detachFd();
        }
        case "vpn.remove": return JSONObject.NULL; // Go closes the sole fd owner.
        case "notification": {
            String text=input.optString("text","FnCPN");ConnectionTileService.changed(this,text.split(" ",2)[0]);((NotificationManager)getSystemService(NOTIFICATION_SERVICE)).notify(1,notification(text));return JSONObject.NULL;
        }
        case "secret.get":case "secret.put":case "secret.delete":case "secret.reset": return credentials.dispatch(method,input);
        default: throw new Exception("Unsupported Android system operation");
        }
    }
    @Override public void onRevoke() { revoked=true; SupportLog.event(this,"vpn_revoked",null); stopSelf(); }
    @Override public void onDestroy() {
        stopping=true;ConnectionTileService.changed(this,"PAUSED");
        SupportLog.event(this,"service_destroy",null);
        connectivity.unregisterNetworkCallback(callback);
        worker.execute(() -> {
            Bridge.stop(revoked);
            if(Bridge.service==this){Bridge.service=null;Bridge.ready=new java.util.concurrent.CountDownLatch(1);}
        });
        stopForeground(STOP_FOREGROUND_REMOVE);super.onDestroy();
    }
}
