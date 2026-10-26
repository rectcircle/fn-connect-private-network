//go:build darwin || ios

#import <Foundation/Foundation.h>
#import <CoreText/CoreText.h>
#import <CoreGraphics/CoreGraphics.h>
#include <math.h>
#include <stdint.h>

@interface FnCPNTextLayout: NSObject
@property(nonatomic,strong) NSArray *lines;
@property(nonatomic,strong) NSArray<NSNumber *> *positions;
@property(nonatomic) double width;
@property(nonatomic) double height;
@property(nonatomic) double baseline;
@end
@implementation FnCPNTextLayout
@end

static FnCPNTextLayout *layout(const char *bytes,size_t length,double size,int flags,int tabs) {
    if(!bytes || !isfinite(size) || size<=0 || size>4096)return nil;
    NSString *text=[[NSString alloc] initWithBytes:bytes length:length encoding:NSUTF8StringEncoding];
    if(!text)return nil;
    static NSCache *cache;static dispatch_once_t once;
    dispatch_once(&once,^{cache=[NSCache new];cache.countLimit=512;cache.totalCostLimit=8*1024*1024;});
    NSString *key=[NSString stringWithFormat:@"%.9g/%d/%d/%@",size,flags,tabs,text];
    FnCPNTextLayout *found=[cache objectForKey:key];if(found)return found;
    CTFontUIFontType type=(flags&4)?kCTFontUIFontUserFixedPitch:kCTFontUIFontSystem;
    CTFontRef font=CTFontCreateUIFontForLanguage(type,size,CFSTR("zh-Hans"));
    if(!font)font=CTFontCreateUIFontForLanguage(kCTFontUIFontSystem,size,NULL);
    if(!font)return nil;
    CTFontSymbolicTraits traits=0;if(flags&1)traits|=kCTFontBoldTrait;if(flags&2)traits|=kCTFontItalicTrait;
    if(traits){CTFontRef styled=CTFontCreateCopyWithSymbolicTraits(font,size,NULL,traits,traits);if(styled){CFRelease(font);font=styled;}}
    // Use a consistent Latin/CJK line box, including for an empty entry or a
    // password made of bullets, so Chinese input cannot exceed its row height.
    CTFontRef cjk=CTFontCreateForString(font,CFSTR("中"),CFRangeMake(0,1));
    double ascent=CTFontGetAscent(font),descent=CTFontGetDescent(font),leading=CTFontGetLeading(font);
    if(cjk){ascent=fmax(ascent,CTFontGetAscent(cjk));descent=fmax(descent,CTFontGetDescent(cjk));leading=fmax(leading,CTFontGetLeading(cjk));CFRelease(cjk);}
    CGFloat interval=10*(tabs>0?tabs:4);
    if(flags&4){UniChar space=' ';CGGlyph glyph;CGSize advance;if(CTFontGetGlyphsForCharacters(font,&space,&glyph,1)){CTFontGetAdvancesForGlyphs(font,kCTFontOrientationHorizontal,&glyph,&advance,1);interval=advance.width*(tabs>0?tabs:4);}}
    NSMutableArray *lines=[NSMutableArray new],*positions=[NSMutableArray new];
    NSArray<NSString *> *parts=[text componentsSeparatedByString:@"\t"];
    double x=0;
    for(NSUInteger i=0;i<parts.count;i++) {
        NSAttributedString *attributed=[[NSAttributedString alloc] initWithString:parts[i] attributes:@{(__bridge id)kCTFontAttributeName:(__bridge id)font,(__bridge id)kCTForegroundColorFromContextAttributeName:@YES}];
        CTLineRef line=CTLineCreateWithAttributedString((__bridge CFAttributedStringRef)attributed);
        CGFloat a=0,d=0,l=0;double width=CTLineGetTypographicBounds(line,&a,&d,&l);
        ascent=fmax(ascent,a);descent=fmax(descent,d);leading=fmax(leading,l);
        [positions addObject:@(x)];[lines addObject:CFBridgingRelease(line)];x+=width;
        if(i+1<parts.count && interval>0)x=floor((x+interval)/interval)*interval;
    }
    CFRelease(font);
    FnCPNTextLayout *result=[FnCPNTextLayout new];result.lines=lines;result.positions=positions;
    result.width=x;result.baseline=ceil(ascent);result.height=result.baseline+ceil(descent+leading);
    if(length<=4096)[cache setObject:result forKey:key cost:length*64+512];
    return result;
}

int fncpn_text_measure(const char *text,size_t length,double size,int flags,int tabs,double *width,double *height,double *baseline) {
 @autoreleasepool {
    FnCPNTextLayout *item=layout(text,length,size,flags,tabs);if(!item)return 0;
    *width=item.width;*height=item.height;*baseline=item.baseline;return 1;
 }
}
int fncpn_text_missing(const char *text,size_t length,double size,int flags,int tabs) {
 @autoreleasepool {
    FnCPNTextLayout *item=layout(text,length,size,flags,tabs);if(!item)return -1;
    int missing=0;
    for(id value in item.lines) {
        NSArray *runs=(__bridge NSArray *)CTLineGetGlyphRuns((__bridge CTLineRef)value);
        for(id runValue in runs) {
            CTRunRef run=(__bridge CTRunRef)runValue;CFIndex count=CTRunGetGlyphCount(run);
            CTFontRef font=(CTFontRef)CFDictionaryGetValue(CTRunGetAttributes(run),kCTFontAttributeName);
            NSString *name=font?CFBridgingRelease(CTFontCopyPostScriptName(font)):@"";
            if([name containsString:@"LastResort"]){missing+=(int)count;continue;}
            CGGlyph *glyphs=calloc((size_t)count,sizeof(CGGlyph));if(count>0&&!glyphs)return -1;
            CTRunGetGlyphs(run,CFRangeMake(0,count),glyphs);for(CFIndex i=0;i<count;i++)if(glyphs[i]==0)missing++;
            free(glyphs);
        }
    }
    return missing;
 }
}
int fncpn_text_draw(const char *text,size_t length,double size,int flags,int tabs,double scale,int offset,uint8_t *pixels,int width,int height,int stride,double red,double green,double blue,double alpha) {
 @autoreleasepool {
    if(!pixels || width<=0 || height<=0 || stride<width*4 || !isfinite(scale) || scale<=0)return 0;
    FnCPNTextLayout *item=layout(text,length,size,flags,tabs);if(!item)return 0;
    CGColorSpaceRef space=CGColorSpaceCreateDeviceRGB();
    CGContextRef context=CGBitmapContextCreate(pixels,width,height,8,stride,space,kCGImageAlphaPremultipliedLast|kCGBitmapByteOrder32Big);CGColorSpaceRelease(space);
    if(!context)return 0;
    CGContextSetShouldAntialias(context,true);CGContextSetShouldSmoothFonts(context,false);
    CGContextScaleCTM(context,scale,scale);
    CGContextSetRGBFillColor(context,red,green,blue,alpha);
    CGContextSetTextMatrix(context,CGAffineTransformIdentity);
    for(NSUInteger i=0;i<item.lines.count;i++){
        CGContextSetTextPosition(context,item.positions[i].doubleValue-(double)offset/scale,(double)height/scale-item.baseline);
        CTLineDraw((__bridge CTLineRef)item.lines[i],context);
    }
    CGContextRelease(context);return 1;
 }
}
