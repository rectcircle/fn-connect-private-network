"""Exercise the actual Java link policy without a device or extra test runtime."""
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]

class NavigationTest(unittest.TestCase):
    def test_local_origin_and_external_links(self):
        with tempfile.TemporaryDirectory() as directory:
            out=Path(directory)
            source=ROOT/'mobile/android/app/src/main/java/cn/rectcircle/fncpn/AdminNavigation.java'
            test=out/'NavigationCheck.java'
            test.write_text('''package cn.rectcircle.fncpn;
public class NavigationCheck {
 public static void main(String[] args) {
  String base="http://127.0.0.1:1234/admin";
  for(String url:new String[]{"http://127.0.0.1:1234/page?q=x#y","http://127.0.0.1:1234/"})
   if(!AdminNavigation.sameOrigin(base,url))throw new AssertionError(url);
  for(String url:new String[]{"http://127.0.0.1:1235/","https://127.0.0.1:1234/","http://localhost:1234/","http://user@127.0.0.1:1234/","https://fncpn.rectcircle.cn/"})
   if(AdminNavigation.sameOrigin(base,url))throw new AssertionError(url);
  for(String url:new String[]{"https://fncpn.rectcircle.cn/#donate","https://github.com/rectcircle/project","http://example.com/path"})
   if(!AdminNavigation.external(url))throw new AssertionError(url);
  for(String url:new String[]{"javascript:alert(1)","file:///tmp/x","intent://x","http://127.0.0.1:1234/","http://192.168.1.2/","http://localhost/","http://x.local/","http://x.localhost/","http://[::1]/","https://user:token@example.com/","not a URL"})
   if(AdminNavigation.external(url))throw new AssertionError(url);
 }
}''')
            subprocess.run(['javac','-d',directory,str(source),str(test)],check=True)
            subprocess.run(['java','-cp',directory,'cn.rectcircle.fncpn.NavigationCheck'],check=True)

if __name__=='__main__':unittest.main()
