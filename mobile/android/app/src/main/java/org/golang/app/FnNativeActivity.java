package org.golang.app;

/** Public inherited entry points for Fyne's native lookup on MainActivity.
 * The upstream driver keeps these methods package-private. This facade avoids
 * patching or copying that driver while our Activity lives in the app package.
 */
public class FnNativeActivity extends GoNativeActivity {
    private static java.lang.ref.WeakReference<FnNativeActivity> current=new java.lang.ref.WeakReference<>(null);
    @Override public void onCreate(android.os.Bundle state){current=new java.lang.ref.WeakReference<>(this);super.onCreate(state);}
    private static android.widget.EditText editor(android.view.View view){if(view instanceof android.widget.EditText)return (android.widget.EditText)view;if(view instanceof android.view.ViewGroup){android.view.ViewGroup group=(android.view.ViewGroup)view;for(int i=0;i<group.getChildCount();i++){android.widget.EditText found=editor(group.getChildAt(i));if(found!=null)return found;}}return null;}

    public static int getRune(int device, int code, int meta) { return GoNativeActivity.getRune(device,code,meta); }
    public static void showKeyboard(int type) {
        GoNativeActivity.showKeyboard(type);
        FnNativeActivity owner=current.get();if(owner==null || (type!=3 && type!=1))return;
        owner.runOnUiThread(()->{android.widget.EditText input=editor(owner.getWindow().getDecorView());if(input==null)return;
            input.setRawInputType(android.text.InputType.TYPE_CLASS_TEXT|(type==3?android.text.InputType.TYPE_TEXT_VARIATION_PASSWORD:android.text.InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD)|android.text.InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS);
            input.setImeHintLocales(new android.os.LocaleList(java.util.Locale.ENGLISH));
            input.setImeOptions(input.getImeOptions()|android.view.inputmethod.EditorInfo.IME_FLAG_FORCE_ASCII|android.view.inputmethod.EditorInfo.IME_FLAG_NO_PERSONALIZED_LEARNING);
            ((android.view.inputmethod.InputMethodManager)owner.getSystemService(android.content.Context.INPUT_METHOD_SERVICE)).restartInput(input);
        });
    }
    public static void hideKeyboard() { GoNativeActivity.hideKeyboard(); }
    public static void showFileOpen(String mimes) { GoNativeActivity.showFileOpen(mimes); }
    public static void showFileSave(String mimes,String filename) { GoNativeActivity.showFileSave(mimes,filename); }
}
