package com.rectcircle.fncpn;

import android.database.Cursor;
import android.database.MatrixCursor;
import android.os.CancellationSignal;
import android.os.ParcelFileDescriptor;
import android.provider.DocumentsContract.Document;
import android.provider.DocumentsContract.Root;
import android.provider.DocumentsProvider;
import java.io.File;
import java.io.FileNotFoundException;

/** System file manager access to local logs, read only. No credentials/config. */
public final class LogDocumentsProvider extends DocumentsProvider {
    private static final String DIRECTORY="logs";
    private File directory(){return new File(getContext().getFilesDir(),"logs");}
    private File file(String id)throws FileNotFoundException{
        if(!id.matches("(client\\.log(\\.[12])?|android\\.log(\\.1)?)"))throw new FileNotFoundException();
        File file=new File(directory(),id);if(!file.isFile())throw new FileNotFoundException();return file;
    }
    @Override public boolean onCreate(){return true;}
    @Override public Cursor queryRoots(String[] projection){
        String[] columns=projection!=null?projection:new String[]{Root.COLUMN_ROOT_ID,Root.COLUMN_DOCUMENT_ID,Root.COLUMN_TITLE,Root.COLUMN_FLAGS,Root.COLUMN_MIME_TYPES,Root.COLUMN_ICON};
        MatrixCursor cursor=new MatrixCursor(columns);MatrixCursor.RowBuilder row=cursor.newRow();
        for(String c:columns){Object value=null;if(c.equals(Root.COLUMN_ROOT_ID)||c.equals(Root.COLUMN_DOCUMENT_ID))value=DIRECTORY;else if(c.equals(Root.COLUMN_TITLE))value="FnCPN 日志";else if(c.equals(Root.COLUMN_FLAGS))value=Root.FLAG_LOCAL_ONLY;else if(c.equals(Root.COLUMN_MIME_TYPES))value="text/plain";else if(c.equals(Root.COLUMN_ICON))value=R.drawable.app_icon;row.add(c,value);}return cursor;
    }
    private MatrixCursor cursor(String[] projection){return new MatrixCursor(projection!=null?projection:new String[]{Document.COLUMN_DOCUMENT_ID,Document.COLUMN_DISPLAY_NAME,Document.COLUMN_MIME_TYPE,Document.COLUMN_SIZE,Document.COLUMN_FLAGS,Document.COLUMN_LAST_MODIFIED});}
    private void add(MatrixCursor cursor,String id)throws FileNotFoundException{
        boolean folder=id.equals(DIRECTORY);File f=folder?directory():file(id);MatrixCursor.RowBuilder row=cursor.newRow();
        for(String c:cursor.getColumnNames()){Object value=null;if(c.equals(Document.COLUMN_DOCUMENT_ID))value=id;else if(c.equals(Document.COLUMN_DISPLAY_NAME))value=folder?"FnCPN 日志":id;else if(c.equals(Document.COLUMN_MIME_TYPE))value=folder?Document.MIME_TYPE_DIR:"text/plain";else if(c.equals(Document.COLUMN_SIZE))value=folder?null:f.length();else if(c.equals(Document.COLUMN_FLAGS))value=0;else if(c.equals(Document.COLUMN_LAST_MODIFIED))value=f.lastModified();row.add(c,value);}
    }
    @Override public Cursor queryDocument(String id,String[] projection)throws FileNotFoundException{MatrixCursor c=cursor(projection);add(c,id);return c;}
    @Override public Cursor queryChildDocuments(String parent,String[] projection,String sort)throws FileNotFoundException{
        if(!DIRECTORY.equals(parent))throw new FileNotFoundException();MatrixCursor c=cursor(projection);for(String name:new String[]{"client.log","client.log.1","client.log.2","android.log","android.log.1"})if(new File(directory(),name).isFile())add(c,name);return c;
    }
    @Override public ParcelFileDescriptor openDocument(String id,String mode,CancellationSignal signal)throws FileNotFoundException{
        if(!"r".equals(mode))throw new FileNotFoundException("Read only");return ParcelFileDescriptor.open(file(id),ParcelFileDescriptor.MODE_READ_ONLY);
    }
}
