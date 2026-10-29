//go:build ios
#import <UIKit/UIKit.h>
#import <NetworkExtension/NetworkExtension.h>
#import <WebKit/WebKit.h>
#import <QuickLook/QuickLook.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#import <TargetConditionals.h>
#include <stdlib.h>
#include <string.h>
#import <objc/runtime.h>

extern void fncpn_ui_back_gesture(void);
extern char *fncpn_ui_translate(char *text);
extern void fncpn_ui_feedback(char *text);
static NSString *uiText(const char *key) {
    char *translated=fncpn_ui_translate((char *)key);
    NSString *text=translated?[NSString stringWithUTF8String:translated]:[NSString stringWithUTF8String:key];
    free(translated);return text;
}
// Duo uses UIKit's adaptive navigation container; other devices keep Fyne chrome.
#include <sys/utsname.h>
int fncpn_ui_duo(void) {
    if (@available(iOS 27.1, *)) {
        const char *model=getenv("SIMULATOR_MODEL_IDENTIFIER");
        struct utsname info;
        if(!model) { uname(&info); model=info.machine; }
        return strcmp(model,"iPhone19,4")==0;
    }
    return 0;
}
extern void fncpn_ui_navigation_tap(void);
extern void fncpn_ui_rail_tap(int index);
static BOOL duoEnabled=NO, duoAdminEnabled=NO, duoExpanded=NO, duoBack=NO;
static NSArray<NSString *> *duoLabels;
static NSArray<UIImage *> *duoIcons;
@interface FnCPNDuoHost : UIViewController
@property UIViewController *content;
@property UIScrollView *rail;
@property UIControl *scrim;
@property UIView *brand;
@property UIImageView *brandIcon;
@property UILabel *brandTitle;
@property CGSize previousLayoutSize;
@property BOOL previousExpanded;
@property BOOL railOnLeft;
@property BOOL previousRailOnLeft;
@property NSMutableArray<UIButton *> *buttons;
@end
static FnCPNDuoHost *duoHost;
@implementation FnCPNDuoHost
- (void)viewDidLoad {
    [super viewDidLoad];
    self.view.backgroundColor=UIColor.systemGroupedBackgroundColor;
    [self addChildViewController:self.content]; [self.view addSubview:self.content.view];
    [self.content didMoveToParentViewController:self];
    self.scrim=[UIControl new]; self.scrim.backgroundColor=[UIColor.blackColor colorWithAlphaComponent:0.28];
    self.scrim.hidden=YES; [self.scrim addTarget:self action:@selector(collapse:) forControlEvents:UIControlEventTouchUpInside];
    [self.view addSubview:self.scrim];
    self.rail=[UIScrollView new]; self.rail.clipsToBounds=YES;
    self.rail.contentInsetAdjustmentBehavior=UIScrollViewContentInsetAdjustmentNever;
    self.brand=[UIView new]; self.brand.userInteractionEnabled=NO;
    self.brandIcon=[UIImageView new]; self.brandIcon.contentMode=UIViewContentModeScaleAspectFit;
    NSArray *iconFiles=[NSBundle.mainBundle objectForInfoDictionaryKey:@"CFBundleIcons"][@"CFBundlePrimaryIcon"][@"CFBundleIconFiles"];
    NSString *iconName=iconFiles.lastObject;
    self.brandIcon.image=[UIImage imageNamed:iconName?:@"AppIcon"];
    self.brandTitle=[UILabel new]; self.brandTitle.text=@"FnCPN";
    self.brandTitle.textColor=UIColor.labelColor; self.brandTitle.font=[UIFont boldSystemFontOfSize:20];
    [self.brand addSubview:self.brandIcon]; [self.brand addSubview:self.brandTitle];
    [self.rail addSubview:self.brand];
    self.rail.backgroundColor=UIColor.systemGroupedBackgroundColor;
    [self.view addSubview:self.rail];
    for(NSNumber *direction in @[@(UISwipeGestureRecognizerDirectionLeft),@(UISwipeGestureRecognizerDirectionRight)]) {
        UISwipeGestureRecognizer *swipe=[[UISwipeGestureRecognizer alloc] initWithTarget:self action:@selector(swipe:)];
        swipe.direction=direction.unsignedIntegerValue; [self.rail addGestureRecognizer:swipe];
    }
    [self rebuild];
}
- (void)swipe:(UISwipeGestureRecognizer *)gesture {
    if(duoBack) return;
    BOOL opens=self.railOnLeft?gesture.direction==UISwipeGestureRecognizerDirectionRight:gesture.direction==UISwipeGestureRecognizerDirectionLeft;
    fncpn_ui_rail_tap(opens?-2:-3);
}
- (void)collapse:(id)sender { fncpn_ui_rail_tap(-3); }
- (void)tap:(UIButton *)sender {
    if(sender.tag==-1 && duoBack) fncpn_ui_navigation_tap();
    else fncpn_ui_rail_tap((int)sender.tag);
}
- (void)rebuild {
    for(UIButton *button in self.buttons) [button removeFromSuperview];
    self.buttons=[NSMutableArray new];
    for(NSUInteger i=0;i<=duoLabels.count;i++) {
        UIButton *button=[UIButton buttonWithType:UIButtonTypeCustom]; button.tag=(NSInteger)i-1;
        button.contentHorizontalAlignment=UIControlContentHorizontalAlignmentLeft;
        button.tintColor=UIColor.labelColor;
        UIImageView *image=[[UIImageView alloc] initWithImage:nil]; image.tag=100;
        image.tintColor=UIColor.labelColor; image.contentMode=UIViewContentModeScaleAspectFit;
        [button addSubview:image];
        UILabel *label=[UILabel new]; label.tag=101; label.textColor=UIColor.labelColor;
        label.font=[UIFont systemFontOfSize:16]; [button addSubview:label];
        [button addTarget:self action:@selector(tap:) forControlEvents:UIControlEventTouchUpInside];
        [self.rail addSubview:button]; [self.buttons addObject:button];
    }
    [self updateState];
}
- (void)updateState {
    for(NSUInteger i=0;i<self.buttons.count;i++) {
        UIButton *button=self.buttons[i];
        NSUInteger iconIndex=i==0?(duoBack?1:0):i+1;
        ((UIImageView *)[button viewWithTag:100]).image=iconIndex<duoIcons.count?duoIcons[iconIndex]:nil;
        NSString *text=i==0?(duoBack?uiText("返回"):uiText("菜单")):duoLabels[i-1];
        ((UILabel *)[button viewWithTag:101]).text=text; button.accessibilityLabel=text;
        button.hidden=(duoBack&&i>0)||(duoExpanded&&!duoBack&&i==0);
        button.enabled=duoEnabled&&(i!=3||duoAdminEnabled);
        button.alpha=button.enabled?1:0.35;
    }
    [self.view setNeedsLayout];
}
- (void)viewDidLayoutSubviews {
    [super viewDidLayoutSubviews];
    CGRect bounds=self.view.bounds; UIEdgeInsets safe=self.view.safeAreaInsets;
    CGFloat width=CGRectGetWidth(bounds), height=CGRectGetHeight(bounds);
    BOOL onLeft=safe.left>safe.right;
    CGFloat railWidth;
    NSMutableArray<NSValue *> *regions=[NSMutableArray new];
    CGRect systemColumn=CGRectNull;
    CGFloat alignmentArea=CGFLOAT_MAX;
    if(@available(iOS 27.1,*)) {
        for(UIViewReservedRegion *region in [self.view reservedRegionsOfKind:UIViewReservedRegionKind.occlusionRegionKind]) {
            if(!region.active) continue;
            CGRect frame=CGRectIntersection(bounds,region.frame);
            if(CGRectIsNull(frame)||CGRectIsEmpty(frame)) continue;
            [regions addObject:[NSValue valueWithCGRect:frame]];
            CGRect core=UIEdgeInsetsInsetRect(region.frame,region.margins);
            CGFloat area=CGRectGetWidth(core)*CGRectGetHeight(core);
            // The system also publishes enclosing avoidance rectangles. Their
            // asymmetric padding must not shift the physical status column.
            if((CGRectGetMidX(core)<width*0.35 || CGRectGetMidX(core)>width*0.65) && CGRectGetWidth(core)<60 && CGRectGetHeight(core)<60 && area>0 && area<alignmentArea) {
                alignmentArea=area; systemColumn=core;
            }
        }
    }
    // Align to the actual camera/status centers, not their asymmetric safety margins.
    if(!CGRectIsNull(systemColumn)) onLeft=CGRectGetMidX(systemColumn)<width/2;
    self.railOnLeft=onLeft;
    // Duo exposes only the padded 84pt status exclusion when unfolded.
    // Its graphical center uses the same edge inset as the physical camera:
    // measured from the public 37pt camera region at x=611 2/3 in a 678pt view.
    // Keep this model-specific fallback inside the existing iPhone19,4 gate.
    const CGFloat duoColumnEdgeInset=47.8333333333333;
    CGFloat iconCenter=CGRectIsNull(systemColumn)?(onLeft?duoColumnEdgeInset:width-duoColumnEdgeInset):CGRectGetMidX(systemColumn);
    CGFloat edgeInset=onLeft?iconCenter:width-iconCenter;
    railWidth=MIN(MAX(2*edgeInset,onLeft?safe.left:safe.right),width/3);
    CGFloat columnX=onLeft?0:width-railWidth;
    CGFloat localIconCenter=MAX(12,MIN(railWidth-12,iconCenter-columnX));
    // Occlusions may be at either end after rotation. Keep the largest free
    // vertical interval instead of placing every icon below the bottom camera.
    CGFloat start=MAX(0,safe.top), end=MAX(start,height-safe.bottom);
    NSMutableArray<NSValue *> *intervals=[NSMutableArray arrayWithObject:[NSValue valueWithCGPoint:CGPointMake(start,end)]];
    for(NSValue *value in regions) {
        CGRect frame=value.CGRectValue;
        if(!CGRectIntersectsRect(frame,CGRectMake(columnX,0,railWidth,height))) continue;
        NSMutableArray<NSValue *> *remaining=[NSMutableArray new];
        for(NSValue *interval in intervals) {
            CGPoint span=interval.CGPointValue;
            CGFloat lo=MAX(span.x,CGRectGetMinY(frame)), hi=MIN(span.y,CGRectGetMaxY(frame));
            if(lo>=hi) { [remaining addObject:interval]; continue; }
            if(lo>span.x) [remaining addObject:[NSValue valueWithCGPoint:CGPointMake(span.x,lo)]];
            if(hi<span.y) [remaining addObject:[NSValue valueWithCGPoint:CGPointMake(hi,span.y)]];
        }
        intervals=remaining;
    }
    CGFloat freeStart=start, freeEnd=start;
    for(NSValue *interval in intervals) {
        CGPoint span=interval.CGPointValue;
        if(span.y-span.x>freeEnd-freeStart) { freeStart=span.x; freeEnd=span.y; }
    }
    CGFloat panelWidth=MIN(300,width-(onLeft?safe.right:safe.left));
    BOOL expanded=duoExpanded&&!duoBack;
    CGFloat visibleWidth=expanded?panelWidth:railWidth;
    self.content.view.frame=CGRectMake(onLeft?railWidth:0,0,width-railWidth,height);
    self.scrim.frame=bounds; self.scrim.hidden=!expanded;
    // The expanded surface fills the display; only its contents avoid occlusions.
    self.rail.frame=CGRectMake(onLeft?0:width-visibleWidth,expanded?0:freeStart,visibleWidth,expanded?height:MAX(0,freeEnd-freeStart));
    self.brand.hidden=!expanded;
    CGFloat brandY=MAX(16,safe.top+12);
    self.brand.frame=CGRectMake(onLeft?railWidth+12:16,brandY,panelWidth-railWidth-28,48);
    self.brandIcon.frame=CGRectMake(0,6,36,36);
    self.brandTitle.frame=CGRectMake(48,0,MAX(0,self.brand.bounds.size.width-48),48);
    CGFloat rowY=expanded?brandY+64:0;
    // Expanded content no longer occupies the collapsed status column on the
    // right. Only avoid regions that actually intersect its icon/text area.
    CGFloat menuContentWidth=panelWidth-(onLeft?0:railWidth);
    if(expanded) {
        BOOL moved;
        do {
            moved=NO;
            CGRect row=CGRectMake(self.rail.frame.origin.x,rowY,menuContentWidth,48);
            for(NSValue *value in regions) {
                CGRect blocked=value.CGRectValue;
                if(CGRectIntersectsRect(row,blocked)) {
                    rowY=CGRectGetMaxY(blocked); moved=YES; break;
                }
            }
        } while(moved);
    }
    for(NSUInteger i=0;i<self.buttons.count;i++) {
        UIButton *button=self.buttons[i];
        button.hidden=(duoBack&&i>0)||(expanded&&i==0);
        button.frame=CGRectMake(0,rowY+(expanded?(i==0?0:i-1):i)*48,panelWidth,48);
        [button viewWithTag:100].frame=CGRectMake(localIconCenter-12,12,24,24);
        [button viewWithTag:101].frame=CGRectMake(railWidth,0,MAX(0,menuContentWidth-railWidth-12),48);
        [button viewWithTag:101].hidden=!expanded;
        ((UILabel *)[button viewWithTag:101]).adjustsFontSizeToFitWidth=YES;
        ((UILabel *)[button viewWithTag:101]).minimumScaleFactor=0.8;
    }
    BOOL resetScroll=!CGSizeEqualToSize(self.previousLayoutSize,bounds.size)||self.previousExpanded!=expanded||self.previousRailOnLeft!=onLeft;
    self.previousLayoutSize=bounds.size; self.previousExpanded=expanded; self.previousRailOnLeft=onLeft;
    CGFloat contentHeight=rowY+(duoBack?1:self.buttons.count-(expanded?1:0))*48;
    self.rail.contentSize=CGSizeMake(visibleWidth,MAX(self.rail.bounds.size.height,contentHeight+(expanded?safe.bottom:0)));
    if(resetScroll) self.rail.contentOffset=CGPointZero;

}
- (UIInterfaceOrientationMask)supportedInterfaceOrientations { return self.content.supportedInterfaceOrientations; }
- (BOOL)shouldAutorotate { return YES; }
@end
UIViewController *fncpn_ui_navigation_host(UIViewController *controller) {
    if(!fncpn_ui_duo()) return controller;
    duoHost=[FnCPNDuoHost new]; duoHost.content=controller; return duoHost;
}
void fncpn_ui_navigation_state(int back,int enabled) {
    dispatch_async(dispatch_get_main_queue(), ^{
        duoBack=back!=0; duoEnabled=enabled!=0; if(duoBack) duoExpanded=NO;
        [duoHost updateState];
    });
}
void fncpn_ui_rail_menu(const char *json) {
    NSData *data=[[NSString stringWithUTF8String:json] dataUsingEncoding:NSUTF8StringEncoding];
    NSDictionary *menu=[NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
    NSMutableArray *icons=[NSMutableArray new];
    for(NSString *encoded in menu[@"icons"]) {
        UIImage *image=[UIImage imageWithData:[[NSData alloc] initWithBase64EncodedString:encoded options:0] scale:3];
        [icons addObject:[image imageWithRenderingMode:UIImageRenderingModeAlwaysTemplate]?:[UIImage new]];
    }
    dispatch_async(dispatch_get_main_queue(), ^{
        duoLabels=menu[@"labels"]; duoIcons=icons; if(duoHost.isViewLoaded) [duoHost rebuild];
    });
}
void fncpn_ui_rail_expanded(int expanded) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [duoHost.view layoutIfNeeded]; duoExpanded=expanded!=0;
        [duoHost.view setNeedsLayout];
        [UIView animateWithDuration:0.2 delay:0 options:UIViewAnimationOptionBeginFromCurrentState|UIViewAnimationOptionCurveEaseOut animations:^{ [duoHost.view layoutIfNeeded]; } completion:nil];
    });
}
void fncpn_ui_rail_admin(int enabled) {
    dispatch_async(dispatch_get_main_queue(), ^{ duoAdminEnabled=enabled!=0; [duoHost updateState]; });
}
static BOOL backGestureEnabled=NO;
@interface FnCPNBackGesture: NSObject<UIGestureRecognizerDelegate>
- (void)attach:(NSNotification *)notification;
- (void)swipe:(UIScreenEdgePanGestureRecognizer *)gesture;
@end
@implementation FnCPNBackGesture
- (void)attach:(NSNotification *)notification {
    UIWindow *window=notification.object;
    if(![window isKindOfClass:UIWindow.class] || window.windowLevel!=UIWindowLevelNormal || objc_getAssociatedObject(window,@selector(attach:))) return;
    UIScreenEdgePanGestureRecognizer *gesture=[[UIScreenEdgePanGestureRecognizer alloc] initWithTarget:self action:@selector(swipe:)];
    gesture.edges=UIRectEdgeLeft; gesture.delegate=self;
    [window addGestureRecognizer:gesture];
    objc_setAssociatedObject(window,@selector(attach:),gesture,OBJC_ASSOCIATION_RETAIN_NONATOMIC);
}
- (BOOL)gestureRecognizerShouldBegin:(UIGestureRecognizer *)recognizer {
    if(!backGestureEnabled || ((UIWindow *)recognizer.view).rootViewController.presentedViewController) return NO;
    CGPoint velocity=[(UIScreenEdgePanGestureRecognizer *)recognizer velocityInView:recognizer.view];
    return velocity.x>0 && velocity.x>fabs(velocity.y);
}
- (void)swipe:(UIScreenEdgePanGestureRecognizer *)gesture {
    if(gesture.state!=UIGestureRecognizerStateEnded || !backGestureEnabled) return;
    CGPoint translation=[gesture translationInView:gesture.view];
    CGPoint velocity=[gesture velocityInView:gesture.view];
    if(translation.x>=60 || (translation.x>=12 && velocity.x>=500)) fncpn_ui_back_gesture();
}
@end
void fncpn_ui_install_back_gesture(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        static FnCPNBackGesture *handler;
        if(!handler) {
            handler=[FnCPNBackGesture new];
            [NSNotificationCenter.defaultCenter addObserver:handler selector:@selector(attach:) name:UIWindowDidBecomeKeyNotification object:nil];
        }
        for(UIScene *scene in UIApplication.sharedApplication.connectedScenes) {
            if([scene isKindOfClass:UIWindowScene.class]) for(UIWindow *window in ((UIWindowScene *)scene).windows)
                [handler attach:[NSNotification notificationWithName:UIWindowDidBecomeKeyNotification object:window]];
        }
    });
}
void fncpn_ui_enable_back_gesture(int enabled) {
    dispatch_async(dispatch_get_main_queue(), ^{ backGestureEnabled=enabled!=0; });
}

static UIViewController *presenter(void) {
    for (UIScene *scene in UIApplication.sharedApplication.connectedScenes) {
        if (![scene isKindOfClass:UIWindowScene.class] || scene.activationState != UISceneActivationStateForegroundActive) continue;
        for (UIWindow *window in ((UIWindowScene *)scene).windows) if (window.isKeyWindow) {
            UIViewController *controller=window.rootViewController;
            while (controller.presentedViewController) controller=controller.presentedViewController;
            return controller;
        }
    }
    return nil;
}

@interface FnCPNLogItem: NSObject<QLPreviewItem>
@property(nonatomic,strong) NSURL *previewItemURL;
@property(nonatomic,copy) NSString *previewItemTitle;
@end
@implementation FnCPNLogItem
@end
@interface FnCPNLogDocuments: NSObject<UIDocumentPickerDelegate,QLPreviewControllerDataSource,QLPreviewControllerDelegate>
@property(nonatomic,strong) FnCPNLogItem *item;
@end
@implementation FnCPNLogDocuments
- (void)documentPicker:(UIDocumentPickerViewController *)controller didPickDocumentsAtURLs:(NSArray<NSURL *> *)urls {
    NSURL *url=urls.firstObject;if(!url)return;
    dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0), ^{
        BOOL scoped=[url startAccessingSecurityScopedResource];
        NSFileCoordinator *coordinator=[[NSFileCoordinator alloc] initWithFilePresenter:nil];
        __block NSData *content=nil;
        [coordinator coordinateReadingItemAtURL:url options:0 error:nil byAccessor:^(NSURL *readURL){
            NSFileHandle *file=[NSFileHandle fileHandleForReadingFromURL:readURL error:nil];
            content=[file readDataUpToLength:2097153 error:nil];[file closeFile];
        }];
        if(scoped)[url stopAccessingSecurityScopedResource];
        NSURL *copy=[NSURL fileURLWithPath:[NSTemporaryDirectory() stringByAppendingPathComponent:[NSString stringWithFormat:@"FnCPN-log-%@.txt",NSUUID.UUID.UUIDString]]];
        BOOL ok=content && content.length<=2097152 && [content writeToURL:copy options:NSDataWritingAtomic error:nil];
        dispatch_async(dispatch_get_main_queue(), ^{
            [controller dismissViewControllerAnimated:YES completion:^{
                UIViewController *parent=presenter();if(!parent)return;
                if(!ok){fncpn_ui_feedback("文件无法读取或超过 2 MiB。");return;}
                self.item=[FnCPNLogItem new];self.item.previewItemURL=copy;self.item.previewItemTitle=url.lastPathComponent;
                QLPreviewController *preview=[QLPreviewController new];preview.dataSource=self;preview.delegate=self;
                [parent presentViewController:preview animated:YES completion:nil];
            }];
        });
    });
}
- (NSInteger)numberOfPreviewItemsInPreviewController:(QLPreviewController *)controller { return self.item?1:0; }
- (id<QLPreviewItem>)previewController:(QLPreviewController *)controller previewItemAtIndex:(NSInteger)index { return self.item; }
- (void)previewControllerDidDismiss:(QLPreviewController *)controller {
    [NSFileManager.defaultManager removeItemAtURL:self.item.previewItemURL error:nil];self.item=nil;
}
@end

char *fncpn_ui_files_directory(int caches) {
 @autoreleasepool {
    NSURL *url=[NSFileManager.defaultManager URLsForDirectory:caches?NSCachesDirectory:NSDocumentDirectory inDomains:NSUserDomainMask].firstObject;
    if(!url || ![NSFileManager.defaultManager createDirectoryAtURL:url withIntermediateDirectories:YES attributes:@{NSFileProtectionKey:NSFileProtectionCompleteUntilFirstUserAuthentication} error:nil])return NULL;
    [url setResourceValue:@YES forKey:NSURLIsExcludedFromBackupKey error:nil];
    return strdup(url.path.fileSystemRepresentation);
 }
}
void fncpn_ui_logs(const char *directory) {
    NSURL *url=[NSURL fileURLWithPath:[NSString stringWithUTF8String:directory] isDirectory:YES];
    dispatch_async(dispatch_get_main_queue(), ^{
        static FnCPNLogDocuments *handler;if(!handler)handler=[FnCPNLogDocuments new];
        UIDocumentPickerViewController *picker=[[UIDocumentPickerViewController alloc] initForOpeningContentTypes:@[UTTypeData] asCopy:NO];
        picker.directoryURL=url;picker.delegate=handler;picker.allowsMultipleSelection=NO;picker.title=uiText("FnCPN 日志快照");
        [presenter() presentViewController:picker animated:YES completion:nil];
    });
}
void fncpn_ui_share_file(const char *path) {
    NSURL *url=[NSURL fileURLWithPath:[NSString stringWithUTF8String:path]];
    dispatch_async(dispatch_get_main_queue(), ^{
        UIViewController *parent=presenter();if(!parent)return;
        UIActivityViewController *share=[[UIActivityViewController alloc] initWithActivityItems:@[url] applicationActivities:nil];
        share.popoverPresentationController.sourceView=parent.view;
        share.popoverPresentationController.sourceRect=CGRectMake(parent.view.bounds.size.width/2,parent.view.bounds.size.height/2,1,1);
        share.completionWithItemsHandler=^(UIActivityType type,BOOL completed,NSArray *items,NSError *error){[NSFileManager.defaultManager removeItemAtURL:url error:nil];};
        [parent presentViewController:share animated:YES completion:nil];
    });
}
@interface FnCPNBrowser: UIViewController<WKNavigationDelegate,WKUIDelegate>
@property(nonatomic,strong) NSURL *url;
@property(nonatomic,strong) WKWebView *web;
@property(nonatomic,strong) UIStackView *loading;
@property(nonatomic,strong) UIActivityIndicatorView *spinner;
@property(nonatomic,strong) UILabel *message;
@property(nonatomic,strong) UIButton *retry;
@property(nonatomic) NSInteger loadGeneration;
@property(nonatomic) BOOL failed;
@end
@implementation FnCPNBrowser
- (void)viewDidLoad {
    [super viewDidLoad];self.view.backgroundColor=UIColor.systemBackgroundColor;
    self.spinner=[[UIActivityIndicatorView alloc] initWithActivityIndicatorStyle:UIActivityIndicatorViewStyleMedium];
    self.message=[UILabel new];self.message.text=uiText("正在打开管理后台…");self.message.numberOfLines=0;self.message.textAlignment=NSTextAlignmentCenter;
    self.retry=[UIButton buttonWithType:UIButtonTypeSystem];[self.retry setTitle:uiText("重试") forState:UIControlStateNormal];[self.retry addTarget:self action:@selector(reload) forControlEvents:UIControlEventTouchUpInside];self.retry.hidden=YES;
    self.loading=[[UIStackView alloc] initWithArrangedSubviews:@[self.spinner,self.message,self.retry]];self.loading.axis=UILayoutConstraintAxisVertical;self.loading.alignment=UIStackViewAlignmentCenter;self.loading.spacing=12;self.loading.translatesAutoresizingMaskIntoConstraints=NO;
    [self.view addSubview:self.loading];[NSLayoutConstraint activateConstraints:@[[self.loading.centerXAnchor constraintEqualToAnchor:self.view.centerXAnchor],[self.loading.centerYAnchor constraintEqualToAnchor:self.view.centerYAnchor],[self.loading.widthAnchor constraintLessThanOrEqualToAnchor:self.view.widthAnchor multiplier:0.85]]];[self.spinner startAnimating];
    self.navigationItem.leftBarButtonItem=[[UIBarButtonItem alloc] initWithBarButtonSystemItem:UIBarButtonSystemItemDone target:self action:@selector(close)];
}
- (void)viewDidAppear:(BOOL)animated {
    [super viewDidAppear:animated];if(self.web)return;
    dispatch_async(dispatch_get_main_queue(), ^{
        if(!self.view.window || self.web)return;
        WKWebViewConfiguration *configuration=[WKWebViewConfiguration new];configuration.websiteDataStore=[WKWebsiteDataStore nonPersistentDataStore];configuration.preferences.javaScriptCanOpenWindowsAutomatically=NO;
        self.web=[[WKWebView alloc] initWithFrame:self.view.bounds configuration:configuration];self.web.autoresizingMask=UIViewAutoresizingFlexibleWidth|UIViewAutoresizingFlexibleHeight;self.web.navigationDelegate=self;self.web.UIDelegate=self;
        [self.view insertSubview:self.web atIndex:0];[self reload];
    });
}
- (void)showLoading {
    self.failed=NO;self.loading.hidden=NO;self.web.hidden=YES;self.message.text=uiText("正在打开管理后台…");self.retry.hidden=YES;[self.spinner startAnimating];
    NSInteger generation=++self.loadGeneration;__weak FnCPNBrowser *weakSelf=self;
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW,30*NSEC_PER_SEC),dispatch_get_main_queue(), ^{FnCPNBrowser *owner=weakSelf;if(owner && generation==owner.loadGeneration && !owner.loading.hidden){[owner.web stopLoading];[owner showFailure];}});
}
- (void)showFailure {self.failed=YES;self.loadGeneration++;self.loading.hidden=NO;self.web.hidden=YES;[self.spinner stopAnimating];self.message.text=uiText("管理后台加载失败，请重试。");self.retry.hidden=NO;}
- (void)reload {[self showLoading];[self.web loadRequest:[NSURLRequest requestWithURL:self.url]];}
- (BOOL)allowsURL:(NSURL *)target {return [target.scheme.lowercaseString isEqualToString:self.url.scheme.lowercaseString] && [target.host.lowercaseString isEqualToString:self.url.host.lowercaseString] && [target.port isEqual:self.url.port] && !target.user && !target.password;}
- (BOOL)externalURL:(NSURL *)target {
    NSString *host=target.host.lowercaseString;
    if(![@[@"http",@"https"] containsObject:target.scheme.lowercaseString] || target.user || target.password || ![host containsString:@"."] || [host hasSuffix:@"."] || [host hasSuffix:@".localhost"] || [host hasSuffix:@".local"] || [host containsString:@":"])return NO;
    return [host rangeOfCharacterFromSet:[[NSCharacterSet characterSetWithCharactersInString:@"0123456789."] invertedSet]].location!=NSNotFound;
}
- (void)webView:(WKWebView *)web decidePolicyForNavigationAction:(WKNavigationAction *)action decisionHandler:(void (^)(WKNavigationActionPolicy))decisionHandler {
    if((!action.targetFrame || action.targetFrame.mainFrame) && ![self allowsURL:action.request.URL]) {
        // WebKit gates new windows by user interaction (automatic JS popups
        // are disabled). Handle their URL once in the UI delegate below.
        if(!action.targetFrame && [self externalURL:action.request.URL]) {decisionHandler(WKNavigationActionPolicyAllow);return;}
        if(action.navigationType==WKNavigationTypeLinkActivated && [self externalURL:action.request.URL]) [UIApplication.sharedApplication openURL:action.request.URL options:@{} completionHandler:nil];
        decisionHandler(WKNavigationActionPolicyCancel);return;
    }
    decisionHandler(WKNavigationActionPolicyAllow);
}
- (void)webView:(WKWebView *)web decidePolicyForNavigationResponse:(WKNavigationResponse *)response decisionHandler:(void (^)(WKNavigationResponsePolicy))decisionHandler {
    if(response.isForMainFrame && ![self allowsURL:response.response.URL]){decisionHandler(WKNavigationResponsePolicyCancel);return;}
    if(response.isForMainFrame && [response.response isKindOfClass:NSHTTPURLResponse.class] && ((NSHTTPURLResponse *)response.response).statusCode>=400){decisionHandler(WKNavigationResponsePolicyCancel);[self showFailure];return;}
    decisionHandler(WKNavigationResponsePolicyAllow);
}
- (void)webView:(WKWebView *)web didStartProvisionalNavigation:(WKNavigation *)navigation {[self showLoading];}
- (void)webView:(WKWebView *)web didFinishNavigation:(WKNavigation *)navigation {if(!self.failed){self.loadGeneration++;self.loading.hidden=YES;self.web.hidden=NO;[self.spinner stopAnimating];}}
- (void)webView:(WKWebView *)web didFailProvisionalNavigation:(WKNavigation *)navigation withError:(NSError *)error {if(error.code!=NSURLErrorCancelled)[self showFailure];}
- (void)webView:(WKWebView *)web didFailNavigation:(WKNavigation *)navigation withError:(NSError *)error {if(error.code!=NSURLErrorCancelled)[self showFailure];}
- (void)webViewWebContentProcessDidTerminate:(WKWebView *)web {[self showFailure];}
- (WKWebView *)webView:(WKWebView *)web createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration forNavigationAction:(WKNavigationAction *)action windowFeatures:(WKWindowFeatures *)features {if([self allowsURL:action.request.URL])[web loadRequest:action.request];else if([self externalURL:action.request.URL])[UIApplication.sharedApplication openURL:action.request.URL options:@{} completionHandler:nil];return nil;}
- (void)close {self.loadGeneration++;[self.web stopLoading];[self dismissViewControllerAnimated:YES completion:nil];}
@end
int fncpn_ui_simulator(void) { return TARGET_OS_SIMULATOR; }
void fncpn_ui_open(const char *input,int embedded) {
    NSString *text=[NSString stringWithUTF8String:input];
    dispatch_async(dispatch_get_main_queue(), ^{
        NSURL *url=[NSURL URLWithString:text];
        if (!url || ![@[@"https",@"http"] containsObject:url.scheme.lowercaseString]) return;
        if(embedded && (![url.scheme isEqualToString:@"http"] || ![url.host isEqualToString:@"127.0.0.1"] || !url.port || url.port.integerValue<=0 || url.user || url.password)) return;
        if (!embedded) { [UIApplication.sharedApplication openURL:url options:@{} completionHandler:nil]; return; }
        FnCPNBrowser *browser=[FnCPNBrowser new]; browser.url=url;
        [presenter() presentViewController:[[UINavigationController alloc] initWithRootViewController:browser] animated:YES completion:nil];
    });
}
void fncpn_ui_share(const char *input) {
    NSString *text=[NSString stringWithUTF8String:input];
    dispatch_async(dispatch_get_main_queue(), ^{
        UIViewController *parent=presenter(); if(!parent) return;
        UIActivityViewController *share=[[UIActivityViewController alloc] initWithActivityItems:@[text] applicationActivities:nil];
        share.popoverPresentationController.sourceView=parent.view;
        share.popoverPresentationController.sourceRect=CGRectMake(parent.view.bounds.size.width/2,parent.view.bounds.size.height/2,1,1);
        [parent presentViewController:share animated:YES completion:nil];
    });
}
// Called from a Go UI worker, never from the UIKit main thread.
char *fncpn_ui_call(const char *input) {
 @autoreleasepool {
    if ([NSThread isMainThread]) return NULL;
    NSData *data=[[NSString stringWithUTF8String:input] dataUsingEncoding:NSUTF8StringEncoding];
    NSDictionary *request=[NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
    if (!request || data.length>262144) return NULL;
    dispatch_semaphore_t done=dispatch_semaphore_create(0);
    __block NSData *reply=nil;
    void (^failure)(NSString *)=^(NSString *message) {
        reply=[[NSJSONSerialization dataWithJSONObject:@{@"version":request[@"version"],@"id":request[@"id"],@"ok":@NO,
            @"error":@{@"code":@"UNAVAILABLE",@"message":message,@"retryable":@YES}} options:0 error:nil] copy];
        dispatch_semaphore_signal(done);
    };
    [NETunnelProviderManager loadAllFromPreferencesWithCompletionHandler:^(NSArray<NETunnelProviderManager *> *managers,NSError *error) {
        if(error){failure(@"Cannot load VPN configuration");return;}
        NSString *identifier=[NSBundle.mainBundle.bundleIdentifier stringByAppendingString:@".PacketTunnel"];
        NETunnelProviderManager *manager=nil;
        for(NETunnelProviderManager *candidate in managers) {
            if([((NETunnelProviderProtocol *)candidate.protocolConfiguration).providerBundleIdentifier isEqualToString:identifier]) {manager=candidate;break;}
        }
        void (^send)(NETunnelProviderManager *)=^(NETunnelProviderManager *configured) {
            NETunnelProviderSession *session=(NETunnelProviderSession *)configured.connection;
            if(session.status!=NEVPNStatusConnected && session.status!=NEVPNStatusReasserting){failure(@"VPN is starting; retry shortly");return;}
            NSError *sendError=nil;
            BOOL sent=[session sendProviderMessage:data returnError:&sendError responseHandler:^(NSData *response) {reply=[response copy];dispatch_semaphore_signal(done);}];
            if(!sent) failure(@"Cannot communicate with VPN provider");
        };
        NSString *method=request[@"method"];
        if([method isEqualToString:@"ios-resume"]) {
            // Wake only an existing, enabled provider. Its shared Manager reads
            // persisted AutoConnect; never send "connect", which overrides pause.
            BOOL pending=NO;
            BOOL running=manager && (manager.connection.status==NEVPNStatusConnected || manager.connection.status==NEVPNStatusReasserting);
            if(manager && manager.enabled &&
               (manager.connection.status==NEVPNStatusDisconnected || manager.connection.status==NEVPNStatusInvalid)) {
                NSError *startError=nil;
                if(![(NETunnelProviderSession *)manager.connection startTunnelWithOptions:@{} andReturnError:&startError]) {
                    failure(@"Cannot resume VPN provider"); return;
                }
                pending=YES;
            } else if(manager && manager.enabled && !running) {
                pending=YES; // The OS may still be tearing down a crashed provider.
            }
            reply=[[NSJSONSerialization dataWithJSONObject:@{@"version":request[@"version"],@"id":request[@"id"],@"ok":@YES,@"result":@{@"pending":@(pending),@"running":@(running)}} options:0 error:nil] copy];
            dispatch_semaphore_signal(done); return;
        }
        if(manager && (manager.connection.status==NEVPNStatusConnected || manager.connection.status==NEVPNStatusReasserting)){send(manager);return;}
        if([method isEqualToString:@"ios-log-snapshot"]) {
            reply=[[NSJSONSerialization dataWithJSONObject:@{@"version":request[@"version"],@"id":request[@"id"],@"ok":@YES,@"result":@{@"available":@NO}} options:0 error:nil] copy];dispatch_semaphore_signal(done);return;
        }
        if([@[@"status",@"diagnose",@"watch-client-status"] containsObject:method]) {
            NSDictionary *status=@{@"state":(manager?@"PAUSED":@"UNCONFIGURED"),@"updatedAt":@"2000-01-01T00:00:00Z",@"dns":@{}};
            id result=([method isEqualToString:@"status"]?status:([method isEqualToString:@"diagnose"]?@{@"status":status}:@{@"status":status,@"changed":@NO,@"generation":@0}));
            reply=[[NSJSONSerialization dataWithJSONObject:@{@"version":request[@"version"],@"id":request[@"id"],@"ok":@YES,@"result":result} options:0 error:nil] copy];dispatch_semaphore_signal(done);return;
        }
        if(!manager){manager=[NETunnelProviderManager new];NETunnelProviderProtocol *protocol=[NETunnelProviderProtocol new];protocol.providerBundleIdentifier=identifier;protocol.serverAddress=@"FnCPN";manager.protocolConfiguration=protocol;manager.localizedDescription=@"FnCPN";}
        manager.enabled=YES;
        NETunnelProviderManager *configured=manager;
        [configured saveToPreferencesWithCompletionHandler:^(NSError *saveError) {
            if(saveError){failure(@"Cannot save VPN configuration");return;}
            [configured loadFromPreferencesWithCompletionHandler:^(NSError *loadError) {
                NSError *startError=nil;
                if(loadError || ![(NETunnelProviderSession *)configured.connection startTunnelWithOptions:@{} andReturnError:&startError]){failure(@"Cannot start VPN provider");return;}
                // Retry the same request once the extension has started, without losing credentials.
                dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0), ^{
                    for(int i=0;i<100;i++) {
                        if(configured.connection.status==NEVPNStatusConnected){send(configured);return;}
                        if(configured.connection.status==NEVPNStatusDisconnected && i>5)break;
                        [NSThread sleepForTimeInterval:0.1];
                    }
                    failure(@"VPN provider did not start");
                });
            }];
        }];
    }];
    if(dispatch_semaphore_wait(done,dispatch_time(DISPATCH_TIME_NOW,45*NSEC_PER_SEC))!=0) return NULL;
    if(!reply || reply.length>262144)return NULL;
    NSString *text=[[NSString alloc] initWithData:reply encoding:NSUTF8StringEncoding];
    char *output=text?strdup(text.UTF8String):NULL;
    return output;
}
}

// Simulator identity lives in the UI app Keychain, separate from the provider.
#import <Security/Security.h>
char *fncpn_sim_directory(void) {
 @autoreleasepool {
    NSURL *base=[NSFileManager.defaultManager URLsForDirectory:NSApplicationSupportDirectory inDomains:NSUserDomainMask].firstObject;
    NSURL *path=[base URLByAppendingPathComponent:@"FnCPNSimulator/Client" isDirectory:YES];
    NSError *error=nil;
    if(![NSFileManager.defaultManager createDirectoryAtURL:path withIntermediateDirectories:YES attributes:@{NSFileProtectionKey:NSFileProtectionCompleteUntilFirstUserAuthentication} error:&error])return NULL;
    [path setResourceValue:@YES forKey:NSURLIsExcludedFromBackupKey error:nil];
    return strdup(path.path.fileSystemRepresentation);
 }
}
char *fncpn_sim_secret(const char *input) {
 @autoreleasepool {
    if(!TARGET_OS_SIMULATOR)return NULL;
    NSData *data=[[NSString stringWithUTF8String:input] dataUsingEncoding:NSUTF8StringEncoding];
    NSDictionary *r=[NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
    NSString *method=r[@"method"],*key=r[@"params"][@"key"];
    if(!key || key.length!=64)return NULL;
    NSMutableDictionary *q=[@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,
        (__bridge id)kSecAttrService:@"cn.rectcircle.fncpn.simulator.v1",(__bridge id)kSecAttrAccount:key} mutableCopy];
    OSStatus status=errSecParam;id result=@{};
    if([method isEqualToString:@"secret.get"]){
        q[(__bridge id)kSecReturnData]=@YES;q[(__bridge id)kSecMatchLimit]=(__bridge id)kSecMatchLimitOne;
        CFTypeRef found=NULL;status=SecItemCopyMatching((__bridge CFDictionaryRef)q,&found);
        NSData *value=CFBridgingRelease(found);
        result=@{@"found":(status==errSecSuccess ? @YES : @NO),@"value":value?[value base64EncodedStringWithOptions:0]:@""};
        if(status==errSecItemNotFound)status=errSecSuccess;
    }else if([method isEqualToString:@"secret.delete"]){
        status=SecItemDelete((__bridge CFDictionaryRef)q);if(status==errSecItemNotFound)status=errSecSuccess;
    }else if([method isEqualToString:@"secret.put"]){
        NSData *value=[[NSData alloc] initWithBase64EncodedString:r[@"params"][@"value"] options:0];
        if(!value)return NULL;
        NSDictionary *attributes=@{(__bridge id)kSecValueData:value,(__bridge id)kSecAttrAccessible:(__bridge id)kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly};
        status=SecItemUpdate((__bridge CFDictionaryRef)q,(__bridge CFDictionaryRef)attributes);
        if(status==errSecItemNotFound){[q addEntriesFromDictionary:attributes];status=SecItemAdd((__bridge CFDictionaryRef)q,NULL);}
    }
    NSData *reply=[NSJSONSerialization dataWithJSONObject:@{@"ok":(status==errSecSuccess ? @YES : @NO),@"status":@(status),@"result":result} options:0 error:nil];
    return reply?strdup([[NSString alloc]initWithData:reply encoding:NSUTF8StringEncoding].UTF8String):NULL;
 }
}
