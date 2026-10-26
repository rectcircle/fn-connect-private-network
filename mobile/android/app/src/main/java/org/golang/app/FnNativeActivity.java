package org.golang.app;

/** Public inherited entry points for Fyne's native lookup on MainActivity.
 * The upstream driver keeps these methods package-private. This facade avoids
 * patching or copying that driver while our Activity lives in the app package.
 */
public class FnNativeActivity extends GoNativeActivity {
    private static java.lang.ref.WeakReference<FnNativeActivity> current=new java.lang.ref.WeakReference<>(null);
    private boolean keyboardAlive;
    private static int keyboardAction;
    private static int keyboardType;
    private static final android.widget.TextView.OnEditorActionListener submitListener=new SubmitListener();
    private static final class SubmitListener implements android.widget.TextView.OnEditorActionListener {
        @Override public boolean onEditorAction(android.widget.TextView view,int action,android.view.KeyEvent event) {
            if(event!=null) {
                if(event.getKeyCode()!=android.view.KeyEvent.KEYCODE_ENTER)return false;
                if(event.getAction()==android.view.KeyEvent.ACTION_DOWN && event.getRepeatCount()==0)cn.rectcircle.fncpn.Bridge.imeSubmit();
                return true; // Consume the matching key-up and repeats without another submission.
            }
            if(action==android.view.inputmethod.EditorInfo.IME_ACTION_NEXT || action==android.view.inputmethod.EditorInfo.IME_ACTION_GO || action==android.view.inputmethod.EditorInfo.IME_ACTION_DONE || (keyboardAction!=0 && action==android.view.inputmethod.EditorInfo.IME_ACTION_UNSPECIFIED)) {
                cn.rectcircle.fncpn.Bridge.imeSubmit();
                return keyboardAction!=0;
            }
            return false;
        }
    }
    private static void configureAction(FnNativeActivity owner,android.widget.EditText input) {
        int old=input.getImeOptions();
        int options=old & ~android.view.inputmethod.EditorInfo.IME_MASK_ACTION;
        options &= ~android.view.inputmethod.EditorInfo.IME_FLAG_NO_ENTER_ACTION;
        if(keyboardAction==1)options|=android.view.inputmethod.EditorInfo.IME_ACTION_NEXT;
        else if(keyboardAction==2)options|=android.view.inputmethod.EditorInfo.IME_ACTION_GO;
        else if(keyboardType!=0)options|=android.view.inputmethod.EditorInfo.IME_ACTION_DONE;
        else options|=android.view.inputmethod.EditorInfo.IME_FLAG_NO_ENTER_ACTION;
        input.setImeOptions(options);
        input.setOnEditorActionListener(submitListener);
        if(old!=options)((android.view.inputmethod.InputMethodManager)owner.getSystemService(android.content.Context.INPUT_METHOD_SERVICE)).restartInput(input);
    }
    public static void setKeyboardAction(int action) {
        FnNativeActivity owner=current.get();if(owner==null)return;
        owner.runOnUiThread(()->{if(cn.rectcircle.fncpn.Bridge.isExiting() || !owner.keyboardAlive || current.get()!=owner || owner.isFinishing() || owner.isDestroyed())return;keyboardAction=action;android.widget.EditText input=editor(owner.getWindow().getDecorView());if(input!=null)configureAction(owner,input);});
    }
    @Override public void onCreate(android.os.Bundle state){current=new java.lang.ref.WeakReference<>(this);keyboardAction=0;keyboardType=0;super.onCreate(state);keyboardAlive=true;}
    @Override public void onDestroy(){keyboardAlive=false;if(current.get()==this){current.clear();keyboardAction=0;keyboardType=0;}super.onDestroy();}
    private static android.widget.EditText editor(android.view.View view){if(view instanceof android.widget.EditText)return (android.widget.EditText)view;if(view instanceof android.view.ViewGroup){android.view.ViewGroup group=(android.view.ViewGroup)view;for(int i=0;i<group.getChildCount();i++){android.widget.EditText found=editor(group.getChildAt(i));if(found!=null)return found;}}return null;}

    public static int getRune(int device, int code, int meta) { return GoNativeActivity.getRune(device,code,meta); }
    public static void showKeyboard(int type) {
        FnNativeActivity owner=current.get();if(owner==null || cn.rectcircle.fncpn.Bridge.isExiting() || !owner.keyboardAlive || owner.isFinishing() || owner.isDestroyed())return;
        GoNativeActivity.showKeyboard(type);
        owner.runOnUiThread(()->{if(cn.rectcircle.fncpn.Bridge.isExiting() || !owner.keyboardAlive || current.get()!=owner || owner.isFinishing() || owner.isDestroyed())return;android.widget.EditText input=editor(owner.getWindow().getDecorView());if(input==null)return;
            keyboardType=type;
            if(type==3 || type==1) {
            input.setRawInputType(android.text.InputType.TYPE_CLASS_TEXT|(type==3?android.text.InputType.TYPE_TEXT_VARIATION_PASSWORD:android.text.InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD)|android.text.InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS);
            input.setImeHintLocales(new android.os.LocaleList(java.util.Locale.ENGLISH));
            input.setImeOptions(input.getImeOptions()|android.view.inputmethod.EditorInfo.IME_FLAG_FORCE_ASCII|android.view.inputmethod.EditorInfo.IME_FLAG_NO_PERSONALIZED_LEARNING);
            ((android.view.inputmethod.InputMethodManager)owner.getSystemService(android.content.Context.INPUT_METHOD_SERVICE)).restartInput(input);
            }
            configureAction(owner,input);
        });
    }
    public static void hideKeyboard() { GoNativeActivity.hideKeyboard(); }
    public static void showFileOpen(String mimes) { GoNativeActivity.showFileOpen(mimes); }
    public static void showFileSave(String mimes,String filename) { GoNativeActivity.showFileSave(mimes,filename); }
}
