#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <UserNotifications/UserNotifications.h>
#import <ServiceManagement/ServiceManagement.h>
extern void fnGoAction(char*);
static NSString *L(NSString *zh, NSString *en) { return [[NSLocale preferredLanguages].firstObject hasPrefix:@"zh"] ? zh : en; }
@interface FnDesktop : NSObject <NSWindowDelegate>
@property(strong) NSStatusItem *status;
@property(strong) NSMenuItem *connection;
@property(strong) NSMenuItem *relogin;
@property(strong) NSMenuItem *autostart;
@property(strong) NSWindow *admin;
@end
@implementation FnDesktop
- (void)action:(NSMenuItem*)sender { fnGoAction((char*)[sender.representedObject UTF8String]); }
- (NSMenuItem*)item:(NSString*)title action:(NSString*)action menu:(NSMenu*)menu {
 NSMenuItem *item=[[NSMenuItem alloc] initWithTitle:title action:@selector(action:) keyEquivalent:@""];
 item.target=self; item.representedObject=action; [menu addItem:item]; return item;
}
- (void)setup {
 self.status=[[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
 NSMenu *menu=[NSMenu new]; menu.autoenablesItems=NO;
 [self item:L(@"打开 FnCPN",@"Open FnCPN") action:@"show" menu:menu]; [menu addItem:[NSMenuItem separatorItem]];
 self.connection=[self item:L(@"连接",@"Connect") action:@"primary" menu:menu];
 self.relogin=[self item:L(@"重新登录",@"Sign in again") action:@"login" menu:menu];
 self.autostart=[self item:L(@"开机自启",@"Launch at login") action:@"autostart" menu:menu];
 [menu addItem:[NSMenuItem separatorItem]];
 [self item:L(@"官网 · 捐赠",@"Website · Donate") action:@"homepage" menu:menu];
 [self item:L(@"退出",@"Quit") action:@"quit" menu:menu]; self.status.menu=menu;
 self.status.button.image=[NSImage imageWithSystemSymbolName:@"link" accessibilityDescription:@"FnCPN"];
 [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
}
- (void)windowWillClose:(NSNotification*)notification { [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory]; }
@end
static FnDesktop *desktop;
void fn_init(void) { dispatch_async(dispatch_get_main_queue(), ^{ desktop=[FnDesktop new]; [desktop setup]; }); }
void fn_dispose(void) { dispatch_async(dispatch_get_main_queue(), ^{ [desktop.admin close]; [[NSStatusBar systemStatusBar] removeStatusItem:desktop.status]; desktop=nil; }); }
void fn_tray(char *state, char *title, char *action, int needsLogin, int busy) {
 NSString *s=[NSString stringWithUTF8String:state], *t=[NSString stringWithUTF8String:title], *a=[NSString stringWithUTF8String:action];
 dispatch_async(dispatch_get_main_queue(), ^{
  NSString *variant=([@[@"LOCAL",@"DIRECT",@"RELAY"] containsObject:s])?@"connected":([@[@"PROBING",@"AUTHORIZING",@"RECONNECTING"] containsObject:s]||busy)?@"working":([@[@"ERROR",@"AUTH_REQUIRED",@"DAEMON_UNAVAILABLE",@"SERVER_UNAVAILABLE"] containsObject:s])?@"attention":@"offline";
  NSString *path=[[NSBundle mainBundle] pathForResource:[@"Status-" stringByAppendingString:variant] ofType:@"png"];
  NSImage *image=path?[[NSImage alloc] initWithContentsOfFile:path]:[NSImage imageWithSystemSymbolName:@"link" accessibilityDescription:t];
  image.size=NSMakeSize(22,18); image.template=YES; desktop.status.button.image=image; desktop.status.button.toolTip=t;
  [desktop.status.button setAccessibilityLabel:t]; desktop.connection.title=a; desktop.connection.enabled=!busy;
  desktop.relogin.hidden=needsLogin; desktop.relogin.enabled=!busy;
  desktop.autostart.state=SMAppService.mainAppService.status==SMAppServiceStatusEnabled?NSControlStateValueOn:NSControlStateValueOff;
 });
}
void fn_admin(char *address) {
 NSString *url=[NSString stringWithUTF8String:address];
 dispatch_async(dispatch_get_main_queue(), ^{
  if(!desktop.admin) {
   desktop.admin=[[NSWindow alloc] initWithContentRect:NSMakeRect(0,0,1000,720) styleMask:NSWindowStyleMaskTitled|NSWindowStyleMaskClosable|NSWindowStyleMaskMiniaturizable|NSWindowStyleMaskResizable backing:NSBackingStoreBuffered defer:NO];
   desktop.admin.title=L(@"FnCPN 管理后台",@"FnCPN Administration"); desktop.admin.minSize=NSMakeSize(640,480); desktop.admin.releasedWhenClosed=NO; desktop.admin.delegate=desktop; [desktop.admin center];
  }
  WKWebView *web=[[WKWebView alloc] initWithFrame:desktop.admin.contentView.bounds]; web.autoresizingMask=NSViewWidthSizable|NSViewHeightSizable;
  // Deliberately no Wails bindings or message handlers in remote administration content.
  desktop.admin.contentView=web; [web loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:url]]];
  [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular]; [desktop.admin makeKeyAndOrderFront:nil]; [NSApp activateIgnoringOtherApps:YES];
 });
}
int fn_autostart(void) { return SMAppService.mainAppService.status==SMAppServiceStatusEnabled; }
char *fn_set_autostart(int enabled) {
 __block NSString *message=@"";
 void (^operation)(void)=^{ NSError *error=nil; if(enabled) [SMAppService.mainAppService registerAndReturnError:&error]; else [SMAppService.mainAppService unregisterAndReturnError:&error]; if(error) message=error.localizedDescription; };
 if([NSThread isMainThread]) operation(); else dispatch_sync(dispatch_get_main_queue(),operation);
 return strdup(message.UTF8String);
}
char *fn_language(void) { return strdup([[NSLocale preferredLanguages].firstObject hasPrefix:@"zh"]?"zh":"en"); }

void fn_notify_root(void) {
 dispatch_async(dispatch_get_main_queue(), ^{
  UNUserNotificationCenter *center=UNUserNotificationCenter.currentNotificationCenter;
  [center requestAuthorizationWithOptions:UNAuthorizationOptionAlert|UNAuthorizationOptionSound completionHandler:^(BOOL granted,NSError *error){
   if(!granted) return;
   UNMutableNotificationContent *content=[UNMutableNotificationContent new];
   content.title=L(@"FnCPN 特权服务不可用",@"FnCPN privileged service unavailable");
   content.body=L(@"连接可能需要特权服务，请确认已安装或重新授权。",@"The connection may require the privileged service. Check installation or authorization.");
   [center addNotificationRequest:[UNNotificationRequest requestWithIdentifier:@"fncpn.privileged.unavailable" content:content trigger:nil] withCompletionHandler:nil];
  }];
 });
}

int fn_login_launch(void) { return [NSWorkspace.sharedWorkspace.frontmostApplication.bundleIdentifier isEqualToString:@"com.apple.loginwindow"]; }

// Use the same SF Symbols as the AppKit application, at Retina resolution.
char *fn_symbols(void) {
 @autoreleasepool {
  NSMutableDictionary *symbols=[NSMutableDictionary new];
  for(NSString *name in @[@"stop.fill",@"bolt.fill",@"person.badge.key",@"arrow.clockwise",@"arrow.left.arrow.right",@"globe",@"doc.on.doc",@"folder",@"power",@"arrow.up.right.square",@"arrow.right",@"arrow.left"]) {
   NSImage *image=[NSImage imageWithSystemSymbolName:name accessibilityDescription:nil];
   image=[image imageWithSymbolConfiguration:[NSImageSymbolConfiguration configurationWithPointSize:14 weight:NSFontWeightRegular]];
   NSBitmapImageRep *bitmap=[[NSBitmapImageRep alloc] initWithBitmapDataPlanes:NULL pixelsWide:32 pixelsHigh:32 bitsPerSample:8 samplesPerPixel:4 hasAlpha:YES isPlanar:NO colorSpaceName:NSDeviceRGBColorSpace bytesPerRow:0 bitsPerPixel:0];
   [NSGraphicsContext saveGraphicsState]; NSGraphicsContext.currentContext=[NSGraphicsContext graphicsContextWithBitmapImageRep:bitmap];
   CGFloat scale=30/MAX(image.size.width,image.size.height);
   NSSize size=NSMakeSize(image.size.width*scale,image.size.height*scale);
   [image drawInRect:NSMakeRect((32-size.width)/2,(32-size.height)/2,size.width,size.height) fromRect:NSZeroRect operation:NSCompositingOperationSourceOver fraction:1];
   [NSGraphicsContext restoreGraphicsState];
   NSData *png=[bitmap representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
   if(png) symbols[name]=[@"data:image/png;base64," stringByAppendingString:[png base64EncodedStringWithOptions:0]];
  }
  NSData *data=[NSJSONSerialization dataWithJSONObject:symbols options:0 error:nil];
  return strdup([[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding].UTF8String);
 }
}
