package cn.rectcircle.fncpn;

import java.net.URI;
import java.util.Locale;

/** Main-frame links only. Never forward cookies, headers or proxy credentials. */
final class AdminNavigation {
    static boolean sameOrigin(String entry,String target) {
        try {
            URI origin=new URI(entry),url=new URI(target);
            return "http".equalsIgnoreCase(url.getScheme()) && "127.0.0.1".equals(url.getHost()) && url.getUserInfo()==null && origin.getPort()>0 && url.getPort()==origin.getPort();
        } catch(Exception invalid){return false;}
    }
    static boolean external(String target) {
        try {
            URI url=new URI(target);String scheme=url.getScheme(),host=url.getHost();
            if(url.getUserInfo()!=null || !("http".equalsIgnoreCase(scheme)||"https".equalsIgnoreCase(scheme)) || host==null)return false;
            host=host.toLowerCase(Locale.ROOT);
            return host.contains(".") && !host.endsWith(".") && !host.endsWith(".localhost") && !host.endsWith(".local") && !host.matches("[0-9.]+") && !host.contains(":") && !host.contains("[");
        } catch(Exception invalid){return false;}
    }
    private AdminNavigation(){}
}
