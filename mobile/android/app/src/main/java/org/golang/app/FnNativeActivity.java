package org.golang.app;

/** Public inherited entry points for Fyne's native lookup on MainActivity.
 * The upstream driver keeps these methods package-private. This facade avoids
 * patching or copying that driver while our Activity lives in the app package.
 */
public class FnNativeActivity extends GoNativeActivity {
    public static int getRune(int device, int code, int meta) { return GoNativeActivity.getRune(device,code,meta); }
    public static void showKeyboard(int type) { GoNativeActivity.showKeyboard(type); }
    public static void hideKeyboard() { GoNativeActivity.hideKeyboard(); }
    public static void showFileOpen(String mimes) { GoNativeActivity.showFileOpen(mimes); }
    public static void showFileSave(String mimes,String filename) { GoNativeActivity.showFileSave(mimes,filename); }
}
