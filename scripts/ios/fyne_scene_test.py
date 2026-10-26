"""Validate the local lifecycle shim without changing the real module cache."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import fyne_scene

class SceneTests(unittest.TestCase):
    def test_local_copy_preserves_upstream_and_redirects_module(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);module=root/'module-cache';source=module/'internal/driver/mobile/app/darwin_ios.m'
            source.parent.mkdir(parents=True)
            original='#import <UIKit/UIKit.h>\n@implementation GoAppAppDelegate\n\tself.window = [[UIWindow alloc] initWithFrame:old];\n\tUNUserNotificationCenter *center;\n@end\n'
            original+='//self.paused = YES;\n//self.resumeOnDidBecomeActive = NO;\n'
            original+='@interface GoAppAppController : GLKViewController<UIContentContainer, GLKViewDelegate>\n@end\n@implementation GoAppAppController\n@end\n'
            original+='void showKeyboard(int keyboardType) {\n        // refresh settings if keyboard is already open\n}\n'
            source.write_text(original)
            (module/'internal/driver/mobile/canvas.go').write_text('package mobile\n')
            mobile=root/'mobile';mobile.mkdir();(mobile/'go.mod').write_text('module example\n');(mobile/'go.sum').write_text('')
            output=root/'output';output.mkdir()
            with patch('fyne_scene.subprocess.check_output',return_value=json.dumps({'Dir':str(module),'Version':'v2.8.2'})),patch('fyne_scene.subprocess.run') as run,patch('fyne_scene.native_text.prepare'),patch('fyne_scene.fyne_mobile_render.prepare'):
                result=fyne_scene.prepare('go',mobile,output,{})
            self.assertEqual(source.read_text(),original)
            self.assertEqual(result.name,'ios.mod')
            modified=(output/'fyne-v2.8.2/internal/driver/mobile/app/darwin_ios.m').read_text()
            self.assertIn('initWithWindowScene:',modified)
            self.assertIn('fncpnNativeBounds',modified)
            self.assertIn('fncpn_ui_navigation_host(app.controller)',modified)
            self.assertIn('UIReturnKeyNext',modified)
            self.assertIn('self.view.safeAreaInsets',modified)
            self.assertNotIn('initWithFrame:old',modified)
            self.assertIn('\nself.paused = YES;',modified)
            self.assertIn('\nself.resumeOnDidBecomeActive = NO;',modified)
            self.assertNotIn('//self.paused',modified)
            self.assertIn('- (void)viewDidLayoutSubviews',modified)
            self.assertIn('@property CGSize fncpnSize;',modified)
            self.assertIn('window.windowScene.interfaceOrientation',modified)
            self.assertIn('UIEdgeInsetsEqualToEdgeInsets',modified)
            self.assertIn('-replace=fyne.io/fyne/v2=',str(run.call_args))

    def test_future_fyne_requires_review(self):
        with patch('fyne_scene.subprocess.check_output',return_value=json.dumps({'Version':'v3.0.0'})):
            with self.assertRaises(SystemExit):fyne_scene.prepare('go',Path('/unused'),Path('/unused'),{})

if __name__=='__main__':unittest.main()
