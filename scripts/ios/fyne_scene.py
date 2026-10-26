"""Local build copy for Fyne 2.8.2's pre-UIScene iOS driver.

Keep the module cache unchanged. Fail on upstream drift so this compatibility
shim cannot silently override a future driver implementation.
"""
import json
from pathlib import Path
import subprocess
import shutil
import native_text
import sys
sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
import fyne_mobile_render

SCENE = r'''
// FnCPN owns a single resizable scene; the existing Fyne controller remains the renderer.
@interface FnCPNSceneDelegate : UIResponder<UIWindowSceneDelegate>
@property (strong, nonatomic) UIWindow *window;
@end
@implementation FnCPNSceneDelegate
- (void)scene:(UIScene *)scene willConnectToSession:(UISceneSession *)session options:(UISceneConnectionOptions *)options {
    if (![scene isKindOfClass:UIWindowScene.class]) return;
    GoAppAppDelegate *app=(GoAppAppDelegate *)UIApplication.sharedApplication.delegate;
    self.window=[[UIWindow alloc] initWithWindowScene:(UIWindowScene *)scene];
    app.window=self.window;
    self.window.rootViewController=fncpn_ui_navigation_host(app.controller);
    [self.window makeKeyAndVisible];
    CGSize size=fncpnNativeBounds();
    int scale=(int)((UIWindowScene *)scene).screen.scale;
    setDisplayMetrics((int)size.width,(int)size.height,scale);
    updateConfig((int)size.width,(int)size.height,((UIWindowScene *)scene).interfaceOrientation);
}
- (void)sceneDidBecomeActive:(UIScene *)scene { lifecycleFocused(); }
- (void)sceneWillResignActive:(UIScene *)scene { lifecycleVisible(); }
- (void)sceneDidEnterBackground:(UIScene *)scene { lifecycleAlive(); }
- (void)sceneWillEnterForeground:(UIScene *)scene { lifecycleVisible(); }
- (void)sceneDidDisconnect:(UIScene *)scene {
    GoAppAppDelegate *app=(GoAppAppDelegate *)UIApplication.sharedApplication.delegate;
    if(app.window==self.window) app.window=nil;
    self.window=nil;
}
@end
'''

LAYOUT = r'''
// UIKit excludes upside-down portrait on iPhones without a Home button.
- (UIInterfaceOrientationMask)supportedInterfaceOrientations {
    return UIDevice.currentDevice.userInterfaceIdiom==UIUserInterfaceIdiomPad
        ? UIInterfaceOrientationMaskAll : UIInterfaceOrientationMaskAllButUpsideDown;
}
- (BOOL)shouldAutorotate { return YES; }
- (void)viewDidLayoutSubviews {
    [super viewDidLayoutSubviews];
    UIWindow *window=self.view.window;
    if (!window) return;
    CGSize size=fncpnNativeBounds();
    UIInterfaceOrientation orientation=window.windowScene.interfaceOrientation;
    UIEdgeInsets insets=fncpn_ui_duo()?self.view.safeAreaInsets:window.safeAreaInsets;
    if (CGSizeEqualToSize(self.fncpnSize, size) &&
        self.fncpnOrientation==orientation && UIEdgeInsetsEqualToEdgeInsets(self.fncpnInsets,insets)) return;
    self.fncpnSize=size;
    self.fncpnOrientation=orientation;
    self.fncpnInsets=insets;
    updateConfig((int)size.width,(int)size.height,orientation);
}
'''


def prepare(go, mobile: Path, output: Path, env):
    module=json.loads(subprocess.check_output([go,'list','-m','-json','fyne.io/fyne/v2'],cwd=mobile,env=env,text=True))
    if module['Version']!='v2.8.2':
        raise SystemExit('Review the UIScene overlay before changing Fyne 2.8.2')
    source=Path(module['Dir'])/'internal/driver/mobile/app/darwin_ios.m'
    original=source.read_text()
    start=original.index('\tself.window = [[UIWindow alloc] initWithFrame:')
    end=original.index('\tUNUserNotificationCenter *center',start)
    replacement=original[:start]+'\tself.controller = [[GoAppAppController alloc] initWithNibName:nil bundle:nil];\n\n'+original[end:]+SCENE
    helper = r"""
// Go's updateConfig expects portrait-normalized pixels and swaps for landscape.
static CGSize fncpnNativeBounds(void) {
    GoAppAppDelegate *app=(GoAppAppDelegate *)UIApplication.sharedApplication.delegate;
    UIWindow *window=app.window;
    if(!window) return UIScreen.mainScreen.nativeBounds.size;
    CGSize size=fncpn_ui_duo()?app.controller.view.bounds.size:window.bounds.size;
    if(UIInterfaceOrientationIsLandscape(window.windowScene.interfaceOrientation)) {
        CGFloat width=size.width; size.width=size.height; size.height=width;
    }
    size.width*=window.screen.scale; size.height*=window.screen.scale;
    return size;
}
"""
    replacement=replacement.replace('#import <UIKit/UIKit.h>', '#import <UIKit/UIKit.h>\nextern int fncpn_ui_duo(void);\nextern UIViewController *fncpn_ui_navigation_host(UIViewController *controller);')
    replacement=replacement.replace('@implementation GoAppAppDelegate',helper+'\n@implementation GoAppAppDelegate',1)
    replacement=replacement.replace('CGSize size = [UIScreen mainScreen].nativeBounds.size;', 'CGSize size = fncpnNativeBounds();')
    # Scene geometry can change without a device orientation change (folding,
    # iPad window resizing). Notify Go after UIKit has laid out the new surface.
    replacement=replacement.replace('@interface GoAppAppController : GLKViewController<UIContentContainer, GLKViewDelegate>',
        '@interface GoAppAppController : GLKViewController<UIContentContainer, GLKViewDelegate>\n@property CGSize fncpnSize;\n@property UIInterfaceOrientation fncpnOrientation;\n@property UIEdgeInsets fncpnInsets;')
    replacement=replacement.replace('@implementation GoAppAppController', '@implementation GoAppAppController\n'+LAYOUT,1)
    replacement=replacement.replace('[[UIApplication sharedApplication] statusBarOrientation]',
        '((GoAppAppDelegate *)UIApplication.sharedApplication.delegate).window.windowScene.interfaceOrientation')
    replacement=replacement.replace('UIWindow *window = UIApplication.sharedApplication.keyWindow;',
        'UIWindow *window = ((GoAppAppDelegate *)UIApplication.sharedApplication.delegate).window;')
    replacement=replacement.replace('UIEdgeInsets inset = window.safeAreaInsets;', 'UIEdgeInsets inset = fncpn_ui_duo()?((GoAppAppDelegate *)UIApplication.sharedApplication.delegate).controller.view.safeAreaInsets:window.safeAreaInsets;')
    # Login entries select Next / Go. Keeping this in the driver also prevents
    # showKeyboard from overwriting the action after a Fyne focus transition.
    keyboard_marker='void showKeyboard(int keyboardType) {'
    if original.count(keyboard_marker)!=1 or original.count('        // refresh settings if keyboard is already open')!=1:
        raise SystemExit('Review keyboard action overlay after upstream changes')
    keyboard_action=r'''
static int fncpnKeyboardAction=0;
void fncpn_set_keyboard_action(int action) {
    dispatch_async(dispatch_get_main_queue(), ^{
        fncpnKeyboardAction=action;
        GoAppAppDelegate *app=(GoAppAppDelegate *)UIApplication.sharedApplication.delegate;
        GoInputView *view=app.controller.inputView;
        view.returnKeyType=action==1?UIReturnKeyNext:(action==2?UIReturnKeyGo:UIReturnKeyDefault);
        [view reloadInputViews];
    });
}
'''
    replacement=replacement.replace(keyboard_marker,keyboard_action+'\n'+keyboard_marker,1)
    replacement=replacement.replace('        // refresh settings if keyboard is already open',
        '        if(fncpnKeyboardAction==1) view.returnKeyType=UIReturnKeyNext;\n        else if(fncpnKeyboardAction==2) view.returnKeyType=UIReturnKeyGo;\n        // refresh settings if keyboard is already open',1)
    # Fyne owns frame scheduling through its needsDraw-gated CADisplayLink.
    # GLKViewController must not also present idle/discarded renderbuffers.
    replacement=replacement.replace('//self.paused = YES;', 'self.paused = YES;')
    replacement=replacement.replace('//self.resumeOnDidBecomeActive = NO;', 'self.resumeOnDidBecomeActive = NO;')
    local=output/'fyne-v2.8.2'
    if not local.exists():
        shutil.copytree(module['Dir'],local)
    native_text.prepare(Path(module['Dir']),local)
    patched=local/'internal/driver/mobile/app/darwin_ios.m'
    patched.chmod(0o644)
    patched.write_text(replacement)
    driver_source=Path(module['Dir'])/'internal/driver/mobile/driver.go'
    if driver_source.exists():
        driver=driver_source.read_text()
        driver=driver.replace('d.currentSize = e', 'if d.currentSize == e { continue }\n\t\t\t\t\td.currentSize = e',1)
        # Geometry changes need relayout/repaint, not recursive theme/font cache invalidation.
        driver=driver.replace('c.Content().Refresh()', 'c.Refresh(c.Content())',1)
        local_driver=local/'internal/driver/mobile/driver.go'
        local_driver.chmod(0o644)
        local_driver.write_text(driver)
    canvas_path=local/'internal/driver/mobile/canvas.go'
    canvas_path.chmod(0o644)
    canvas_path.write_text((Path(module['Dir'])/'internal/driver/mobile/canvas.go').read_text())
    fyne_mobile_render.prepare(Path(module['Dir']),local)
    modfile=output/'ios.mod'
    modfile.write_text((mobile/'go.mod').read_text())
    shutil.copy2(mobile/'go.sum',output/'ios.sum')
    subprocess.run([go,'mod','edit','-modfile',str(modfile),
                    '-replace=fyne.io/fyne/v2='+str(local),
                    '-replace=github.com/rectcircle/fn-connect-private-network='+str(mobile.parent)],cwd=mobile,env=env,check=True)
    return modfile
