package cn.rectcircle.fncpn;

import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Path;
import android.view.View;

/** Stroke icons have no Button background, default inset, divider or elevation. */
final class ChromeIcon extends View {
    private final Paint paint=new Paint(Paint.ANTI_ALIAS_FLAG);
    ChromeIcon(Context context,int color){super(context);paint.setColor(color);paint.setStyle(Paint.Style.STROKE);paint.setStrokeWidth(1.8f);paint.setStrokeCap(Paint.Cap.ROUND);paint.setStrokeJoin(Paint.Join.ROUND);}
    @Override protected void onDraw(Canvas canvas){
        super.onDraw(canvas);canvas.save();float scale=Math.min(getWidth(),getHeight())/24f;canvas.translate((getWidth()-24*scale)/2,(getHeight()-24*scale)/2);canvas.scale(scale,scale);
        Path p=new Path();
        p.moveTo(15,5);p.lineTo(8,12);p.lineTo(15,19);
        canvas.drawPath(p,paint);canvas.restore();
    }
}
