// Native settings share the Win32 control and IPC stack with the main window.
constexpr UINT kSettingsUpdate = WM_APP + 80;
constexpr int kSettingsGeneral=8100,kSettingsLinks=8101,kSettingsAuto=8102,kSettingsOrder=8103,kSettingsReset=8104,kSettingsChoose=8105,kSettingsIPv6=8106,kSettingsRelay=8107,kSettingsStart=8108,kSettingsCancel=8109,kSettingsDone=8110;
std::atomic<UINT_PTR> g_nextSettingsInstance{1};
struct SettingsPlacement { HWND window; int x,y,width,height; };
struct NativeThroughputPoint { double elapsed; double rate; };
struct NativeSettingsState {
 HWND window{},list{},notice{},selector{},start{},cancelButton{};
 HWND linkSwitches[3]{};
 HWND choicesWindow{}, brand{}, autoLabel{}, pageTitle{}, policyTitle{}, testTitle{}, contextLabel{}, startupTitle{}, aboutTitle{}, commitLabel{}, tooltip{};
 HICON appIcon{}; HWND iconView{}; std::wstring helpText;
 HIMAGELIST dragImage{};
 HFONT font{},headingFont{},titleFont{},paintFont{},paintHeading{},paintMetric{};
 HIMAGELIST rowHeight{};
 std::vector<SettingsPlacement> placements;
 int scroll=0, laidOutScroll=-1, contentWidth=960, contentHeight=720, documentHeight=0;
 bool layingOut=false;
 bool saving=false, saveCompleted=false;
 std::vector<HWND> general,links,choices;
 // Only the mailbox is shared with workers. JSON models, HWND layout and
 // painting belong exclusively to the window thread.
 const UINT_PTR instanceId=g_nextSettingsInstance.fetch_add(1);
 std::mutex mutex;
 std::wstring pendingStatus,pendingFailure,pendingBuildInfo,pendingCommit;
 bool pendingFinished=false,pendingFailureSet=false;
 JsonObject preferences{},status{};
 std::wstring failure;
 std::wstring buildInfo;
 std::wstring commit;
 HWND buildLabel{};
 bool active=false, workerRunning=false;
 bool choosing=false, linkPage=false, renderingRows=false;
 std::wstring pendingPreferences;
 int dragging=-1;
 HANDLE cancellation=CreateEventW(nullptr,TRUE,FALSE,nullptr);
 std::vector<NativeThroughputPoint> samples;
 double sampleWindow=8000;
 std::wstring sampleLink;
 std::wstring sampleStage;
 ~NativeSettingsState(){CloseHandle(cancellation);for(auto f:{font,headingFont,titleFont,paintFont,paintHeading,paintMetric})if(f)DeleteObject(f);if(rowHeight)ImageList_Destroy(rowHeight);if(appIcon)DestroyIcon(appIcon);}
};
HWND g_nativeSettings{};
std::wstring NativeLinkName(const std::wstring& link){return link==L"ipv6"?L("IPv6 直连"):L("FN Connect 中继");}
constexpr int kSettingsBaseWidth=960;
int SettingsScale(HWND window,int value){return MulDiv(value,GetDpiForWindow(window),96);}
void SettingsLayout(const std::shared_ptr<NativeSettingsState>& state,bool bottom=false);
void SettingsFonts(const std::shared_ptr<NativeSettingsState>& state){
 auto make=[&](int height,int weight,bool scaled){return CreateFontW(-(scaled?SettingsScale(state->window,height):height),0,0,0,weight,FALSE,FALSE,FALSE,DEFAULT_CHARSET,OUT_DEFAULT_PRECIS,CLIP_DEFAULT_PRECIS,CLEARTYPE_QUALITY,DEFAULT_PITCH,L"Segoe UI");};
 auto old=std::array<HFONT,6>{state->font,state->headingFont,state->titleFont,state->paintFont,state->paintHeading,state->paintMetric};
 state->font=make(14,FW_NORMAL,true);state->headingFont=make(17,FW_SEMIBOLD,true);state->titleFont=make(26,FW_SEMIBOLD,true);
 state->paintFont=make(14,FW_NORMAL,false);state->paintHeading=make(17,FW_SEMIBOLD,false);state->paintMetric=make(28,FW_SEMIBOLD,false);
 for(auto placement:state->placements)SendMessageW(placement.window,WM_SETFONT,reinterpret_cast<WPARAM>(state->font),TRUE);
 if(state->brand)SendMessageW(state->brand,WM_SETFONT,reinterpret_cast<WPARAM>(state->titleFont),TRUE);
 for(auto control:{state->pageTitle,state->policyTitle,state->testTitle,state->startupTitle,state->aboutTitle})if(control)SendMessageW(control,WM_SETFONT,reinterpret_cast<WPARAM>(control==state->pageTitle?state->titleFont:state->headingFont),TRUE);
 if(state->tooltip)SendMessageW(state->tooltip,WM_SETFONT,reinterpret_cast<WPARAM>(state->font),TRUE);
 for(auto f:old)if(f)DeleteObject(f);
 if(state->iconView){auto previous=state->appIcon;state->appIcon=static_cast<HICON>(LoadImageW(GetModuleHandleW(nullptr),MAKEINTRESOURCEW(1),IMAGE_ICON,SettingsScale(state->window,48),SettingsScale(state->window,48),0));SendMessageW(state->iconView,STM_SETICON,reinterpret_cast<WPARAM>(state->appIcon),0);if(previous)DestroyIcon(previous);}
 if(state->list){auto previous=state->rowHeight;state->rowHeight=ImageList_Create(1,SettingsScale(state->window,52),ILC_COLOR32,1,1);ListView_SetImageList(state->list,state->rowHeight,LVSIL_SMALL);if(previous)ImageList_Destroy(previous);}
}
LRESULT CALLBACK SettingsControlProc(HWND control,UINT message,WPARAM wParam,LPARAM lParam,UINT_PTR,DWORD_PTR){
 if(message==WM_LBUTTONDOWN)SendMessageW(control,WM_CHANGEUISTATE,MAKEWPARAM(UIS_SET,UISF_HIDEFOCUS),0);
 if(message==WM_MOUSEMOVE&&IsWindowEnabled(control)){wchar_t name[32]{};GetClassNameW(control,name,32);if(_wcsicmp(name,L"Button")==0)SetCursor(LoadCursorW(nullptr,IDC_HAND));}
 if(message==WM_MOUSEWHEEL)return SendMessageW(GetParent(control),message,wParam,lParam);
 if(message==WM_SETCURSOR&&LOWORD(lParam)==HTCLIENT&&IsWindowEnabled(control)){
  wchar_t name[32]{};GetClassNameW(control,name,32);if(_wcsicmp(name,L"Button")==0){SetCursor(LoadCursorW(nullptr,IDC_HAND));return TRUE;}
 }
 return DefSubclassProc(control,message,wParam,lParam);
}
HWND SettingsControl(HWND parent,const wchar_t* klass,const wchar_t* text,DWORD style,int id,int x,int y,int w,int h){
 auto holder=reinterpret_cast<std::shared_ptr<NativeSettingsState>*>(GetWindowLongPtrW(parent,GWLP_USERDATA));
 auto child=CreateWindowExW(0,klass,text,WS_CHILD|WS_VISIBLE|WS_CLIPSIBLINGS|style,x,y,w,h,parent,reinterpret_cast<HMENU>(static_cast<INT_PTR>(id)),GetModuleHandleW(nullptr),nullptr);
 SetWindowSubclass(child,SettingsControlProc,1,0);
 if(holder){(*holder)->placements.push_back({child,x,y,w,h});SendMessageW(child,WM_SETFONT,reinterpret_cast<WPARAM>((*holder)->font),TRUE);}return child;
}
void SettingsNotify(const std::shared_ptr<NativeSettingsState>& state){std::lock_guard lock(state->mutex);if(state->window)PostMessageW(state->window,kSettingsUpdate,state->instanceId,0);}
struct SettingsWorkerApartment {SettingsWorkerApartment(){winrt::init_apartment(winrt::apartment_type::multi_threaded);}~SettingsWorkerApartment(){winrt::uninit_apartment();}};
void SettingsSave(const std::shared_ptr<NativeSettingsState>& state){
#ifdef FNCPN_NATIVE_TEST
 (void)state;
#else
 if(state->saving)return;state->saving=true;std::wstring encodedPreferences=state->preferences.Stringify().c_str();
 for(auto control:state->linkSwitches)EnableWindow(control,FALSE);EnableWindow(GetDlgItem(state->window,kSettingsReset),FALSE);
 StartWorker([state,encodedPreferences]{try{SettingsWorkerApartment apartment;auto preferences=JsonObject::Parse(encodedPreferences);auto saved=CallIpc(L"set-link-settings",&preferences).GetNamedObject(L"result");std::lock_guard lock(state->mutex);state->pendingPreferences=saved.Stringify().c_str();state->pendingFailure.clear();state->pendingFailureSet=true;state->saveCompleted=true;}catch(const winrt::hresult_error& e){std::lock_guard lock(state->mutex);state->pendingFailure=e.message().c_str();state->pendingFailureSet=true;state->saveCompleted=true;}catch(const std::exception&){std::lock_guard lock(state->mutex);state->pendingFailure=L("设置页面更新失败，请关闭后重试。");state->pendingFailureSet=true;state->saveCompleted=true;}SettingsNotify(state);});
#endif
}
void SettingsContext(const std::shared_ptr<NativeSettingsState>& state){
 auto nas=JsonString(state->preferences,L"fnId");auto path=state->active?JsonString(state->status,L"link"):g_remoteState==L"DIRECT"?L"ipv6":g_remoteState==L"RELAY"?L"fn-connect":g_remoteState==L"LOCAL"?L"local":L"";
 std::wstring text=nas.empty()?L("请先登录 NAS"):std::wstring(L("当前 NAS"))+L"："+nas+L"   ·   "+(path.empty()?L("未连接"):path==L"local"?L("局域网"):NativeLinkName(path));
 if(GetText(state->contextLabel)!=text)SetWindowTextW(state->contextLabel,text.c_str());
}
void SettingsRows(const std::shared_ptr<NativeSettingsState>& state){
 state->renderingRows=true;
 ListView_DeleteAllItems(state->list);auto links=state->preferences.GetNamedArray(L"links",JsonArray{});
 for(int i=0;i<3;i++){
  std::wstring label=i==0?L("局域网 · 始终优先"):links.Size()==2?NativeLinkName(JsonString(links.GetObjectAt(i-1),L"link")):NativeLinkName(i==1?L"ipv6":L"fn-connect");
  SetWindowTextW(state->linkSwitches[i],label.c_str());label=L"⠿   "+label;
  LVITEMW row{};row.mask=LVIF_TEXT;row.iItem=i;row.pszText=label.data();ListView_InsertItem(state->list,&row);
  EnableWindow(state->linkSwitches[i],i>0&&!state->saving&&!JsonString(state->preferences,L"fnId").empty());InvalidateRect(state->linkSwitches[i],nullptr,TRUE);
 }
 SettingsLayout(state);
 state->renderingRows=false;SettingsContext(state);
}
void SettingsPage(const std::shared_ptr<NativeSettingsState>& state,bool links){
 state->linkPage=links;state->scroll=0;state->choosing=false;
 for(auto control:state->general)ShowWindow(control,links?SW_HIDE:SW_SHOW);
 for(auto control:state->links)ShowWindow(control,links?SW_SHOW:SW_HIDE);
 ShowWindow(state->choicesWindow,SW_HIDE);
 SetWindowTextW(state->pageTitle,links?L("网络链路"):L("通用"));
 for(int id:{kSettingsGeneral,kSettingsLinks})InvalidateRect(GetDlgItem(state->window,id),nullptr,FALSE);
 SettingsLayout(state);InvalidateRect(state->window,nullptr,FALSE);
}
int SettingsMessageHeight(const std::shared_ptr<NativeSettingsState>& state,const std::wstring& message){
 if(message.empty())return 0;
 auto dc=GetDC(state->window);auto old=SelectObject(dc,state->paintFont);RECT box{0,0,std::max(200,state->contentWidth-336),0};DrawTextW(dc,message.c_str(),-1,&box,DT_CALCRECT|DT_WORDBREAK|DT_NOPREFIX);SelectObject(dc,old);ReleaseDC(state->window,dc);return box.bottom+12;
}
int SettingsResultHeight(const std::shared_ptr<NativeSettingsState>& state,const JsonObject& result){auto message=JsonString(result,L"message");if(message.empty())message=JsonString(result,L"error");return 64+SettingsMessageHeight(state,message);}
int SettingsDocumentHeight(const std::shared_ptr<NativeSettingsState>& state){
 if(!state->linkPage)return 600;
 int height=state->active?820:510;
 auto results=state->status.GetNamedArray(L"results",JsonArray{});
 if(results.Size())height+=36;for(auto value:results)height+=SettingsResultHeight(state,value.GetObject());
 if(!state->failure.empty())height+=SettingsMessageHeight(state,state->failure);
 return height+32;
}
void SettingsVisible(HWND control,bool visible){if(control&&((GetWindowLongW(control,GWL_STYLE)&WS_VISIBLE)!=0)!=visible)ShowWindow(control,visible?SW_SHOW:SW_HIDE);}
void SettingsLayout(const std::shared_ptr<NativeSettingsState>& state,bool bottom){
 // SetScrollInfo can synchronously send WM_SIZE. Never nest child layout.
 if(state->layingOut)return;
 struct LayoutScope { bool& flag; explicit LayoutScope(bool& value):flag(value){flag=true;} ~LayoutScope(){flag=false;} } scope(state->layingOut);
 int oldWidth=state->contentWidth,oldHeight=state->contentHeight,oldScroll=state->laidOutScroll;
 RECT client{};GetClientRect(state->window,&client);int dpi=GetDpiForWindow(state->window);
 state->contentWidth=MulDiv(client.right,96,dpi);state->contentHeight=MulDiv(client.bottom,96,dpi);
 int document=SettingsDocumentHeight(state),maximum=std::max(0,document-state->contentHeight);
 state->scroll=bottom?maximum:std::clamp(state->scroll,0,maximum);
 bool changed=oldWidth!=state->contentWidth||oldHeight!=state->contentHeight||oldScroll!=state->scroll||state->documentHeight!=document;state->documentHeight=document;state->laidOutScroll=state->scroll;
 SCROLLINFO info{sizeof(info),SIF_RANGE|SIF_PAGE|SIF_POS};info.nMin=0;info.nMax=document-1;info.nPage=state->contentHeight;info.nPos=state->scroll;SCROLLINFO oldInfo{sizeof(oldInfo),SIF_ALL};GetScrollInfo(state->window,SB_VERT,&oldInfo);
 if(oldInfo.nMax!=info.nMax||oldInfo.nPage!=info.nPage||oldInfo.nPos!=info.nPos)SetScrollInfo(state->window,SB_VERT,&info,TRUE);
 int extra=state->contentWidth-kSettingsBaseWidth;
 for(const auto& p:state->placements){
  int id=GetDlgCtrlID(p.window),x=p.x,w=p.width;
  bool fixed=id==kSettingsGeneral||id==kSettingsLinks;
  if(!fixed){if(w>=280)w+=extra;if(id==8111||(id>=8140&&id<=8142))x+=extra;}
  int y=p.y-(fixed?0:state->scroll);
  RECT now{};GetWindowRect(p.window,&now);MapWindowPoints(nullptr,state->window,reinterpret_cast<POINT*>(&now),2);
  int px=SettingsScale(state->window,x),py=SettingsScale(state->window,y),pw=SettingsScale(state->window,w),ph=SettingsScale(state->window,p.height);
  if(now.left!=px||now.top!=py||now.right-now.left!=pw||now.bottom-now.top!=ph)SetWindowPos(p.window,nullptr,px,py,pw,ph,SWP_NOZORDER|SWP_NOACTIVATE|SWP_NOCOPYBITS);
 }
 if(state->list){int width=SettingsScale(state->window,state->contentWidth-384);if(ListView_GetColumnWidth(state->list,0)!=width)ListView_SetColumnWidth(state->list,0,width);}
 SettingsVisible(state->start,state->linkPage&&!state->active);SettingsVisible(state->cancelButton,state->linkPage&&state->active);
 // The live caption is painted in the same back buffer as the diagram.
 if(state->notice)SettingsVisible(state->notice,false);
 int errorY=state->active?820:510;auto results=state->status.GetNamedArray(L"results",JsonArray{});if(results.Size())errorY+=36;for(auto value:results)errorY+=SettingsResultHeight(state,value.GetObject());
 auto dismiss=GetDlgItem(state->window,8113);SetWindowPos(dismiss,nullptr,SettingsScale(state->window,state->contentWidth-76),SettingsScale(state->window,errorY+std::max(0,(SettingsMessageHeight(state,state->failure)-28)/2)-state->scroll),SettingsScale(state->window,28),SettingsScale(state->window,28),SWP_NOZORDER|SWP_NOACTIVATE);SettingsVisible(dismiss,state->linkPage&&!state->failure.empty());
 // Keep the copy glyph next to the full, selectable revision text.
 if(state->commitLabel){
  auto dc=GetDC(state->commitLabel);auto old=SelectObject(dc,state->font);SIZE size{};auto text=GetText(state->commitLabel);GetTextExtentPoint32W(dc,text.c_str(),static_cast<int>(text.size()),&size);SelectObject(dc,old);ReleaseDC(state->commitLabel,dc);
  int x=std::min(state->contentWidth-80,240+MulDiv(size.cx,96,dpi)+8);
  SetWindowPos(state->commitLabel,nullptr,SettingsScale(state->window,240),SettingsScale(state->window,384-state->scroll),SettingsScale(state->window,std::max(120,x-248)),SettingsScale(state->window,28),SWP_NOZORDER|SWP_NOACTIVATE);
  SetWindowPos(GetDlgItem(state->window,8112),nullptr,SettingsScale(state->window,x),SettingsScale(state->window,380-state->scroll),SettingsScale(state->window,28),SettingsScale(state->window,28),SWP_NOZORDER|SWP_NOACTIVATE);
 }
 // Moving children only invalidates exposed fragments; the parent draws cards,
 // graphs and results in document coordinates and must repaint the whole viewport.
 if(changed)RedrawWindow(state->window,nullptr,nullptr,RDW_INVALIDATE|RDW_ALLCHILDREN);
}
COLORREF SettingsTextColor(){return g_highContrast?GetSysColor(COLOR_WINDOWTEXT):RGB(35,38,43);}
void SettingsCard(HDC dc,int x,int y,int width,int height){
 auto brush=CreateSolidBrush(GetSysColor(COLOR_WINDOW));auto pen=CreatePen(PS_SOLID,1,g_highContrast?GetSysColor(COLOR_WINDOWTEXT):RGB(224,227,232));
 auto oldBrush=SelectObject(dc,brush);auto oldPen=SelectObject(dc,pen);RoundRect(dc,x,y,x+width,y+height,12,12);SelectObject(dc,oldBrush);SelectObject(dc,oldPen);DeleteObject(brush);DeleteObject(pen);
}
void SettingsEndDrag(const std::shared_ptr<NativeSettingsState>& state){
 if(state->dragImage){ImageList_DragLeave(nullptr);ImageList_EndDrag();ImageList_Destroy(state->dragImage);state->dragImage=nullptr;}
 state->dragging=-1;ListView_SetItemState(state->list,-1,0,LVIS_DROPHILITED);
}
bool SettingsBeginDrag(const std::shared_ptr<NativeSettingsState>& state,int row){
 RECT bounds{},listBounds{};ListView_GetItemRect(state->list,row,&bounds,LVIR_BOUNDS);GetWindowRect(state->list,&listBounds);
 int width=SettingsScale(state->window,state->contentWidth-288),height=SettingsScale(state->window,52);
 auto screen=GetDC(state->window),dc=CreateCompatibleDC(screen);auto bitmap=CreateCompatibleBitmap(screen,width,height);auto oldBitmap=SelectObject(dc,bitmap);RECT r{0,0,width,height};FillRect(dc,&r,GetSysColorBrush(COLOR_WINDOW));
 auto pen=CreatePen(PS_SOLID,1,RGB(0,120,215));auto oldPen=SelectObject(dc,pen);auto oldBrush=SelectObject(dc,GetStockObject(NULL_BRUSH));RoundRect(dc,0,0,width,height,SettingsScale(state->window,8),SettingsScale(state->window,8));SelectObject(dc,oldBrush);SelectObject(dc,oldPen);DeleteObject(pen);
 wchar_t label[256]{};ListView_GetItemText(state->list,row,0,label,256);auto oldFont=SelectObject(dc,state->font);SetBkMode(dc,TRANSPARENT);SetTextColor(dc,SettingsTextColor());r.left=SettingsScale(state->window,12);r.right-=SettingsScale(state->window,64);DrawTextW(dc,label,-1,&r,DT_SINGLELINE|DT_VCENTER|DT_NOPREFIX);SelectObject(dc,oldFont);
 // Keep the entire draggable row, including its enabled switch, under the pointer.
 auto links=state->preferences.GetNamedArray(L"links",JsonArray{});bool on=links.Size()==2&&JsonBool(links.GetObjectAt(row-1),L"enabled");int x=width-SettingsScale(state->window,48),y=(height-SettingsScale(state->window,22))/2;auto track=CreateSolidBrush(on?RGB(0,120,215):GetSysColor(COLOR_GRAYTEXT));oldBrush=SelectObject(dc,track);oldPen=SelectObject(dc,GetStockObject(NULL_PEN));RoundRect(dc,x,y,x+SettingsScale(state->window,40),y+SettingsScale(state->window,22),SettingsScale(state->window,22),SettingsScale(state->window,22));SelectObject(dc,GetStockObject(WHITE_BRUSH));int knob=x+SettingsScale(state->window,on?20:2);Ellipse(dc,knob,y+SettingsScale(state->window,2),knob+SettingsScale(state->window,18),y+SettingsScale(state->window,20));SelectObject(dc,oldBrush);SelectObject(dc,oldPen);DeleteObject(track);
 state->dragImage=ImageList_Create(width,height,ILC_COLOR24|ILC_MASK,1,0);ImageList_AddMasked(state->dragImage,bitmap,RGB(255,0,255));SelectObject(dc,oldBitmap);DeleteObject(bitmap);DeleteDC(dc);ReleaseDC(state->window,screen);
 POINT cursor{};GetCursorPos(&cursor);int hotX=std::clamp(static_cast<int>(cursor.x-listBounds.left),0,width-1),hotY=std::clamp(static_cast<int>(cursor.y-listBounds.top-bounds.top),0,height-1);
 if(!ImageList_BeginDrag(state->dragImage,0,hotX,hotY)){ImageList_Destroy(state->dragImage);state->dragImage=nullptr;return false;}
 ImageList_DragEnter(nullptr,cursor.x,cursor.y);state->dragging=row;SetCapture(state->window);return true;
}
double SettingsNumber(const JsonObject& object,const wchar_t* key,double fallback=0){
 auto value=object.GetNamedValue(key,nullptr);if(!value||value.ValueType()!=JsonValueType::Number)return fallback;
 auto number=value.GetNumber();return std::isfinite(number)?number:fallback;
}
struct SettingsPaintBuffer {
 HWND window; PAINTSTRUCT paint{}; HDC target{},dc{}; HBITMAP bitmap{},previous{}; RECT pixels{};int saved=0;
 explicit SettingsPaintBuffer(HWND w):window(w){
  target=BeginPaint(window,&paint);GetClientRect(window,&pixels);if(!target)return;
  auto memory=CreateCompatibleDC(target);auto image=CreateCompatibleBitmap(target,std::max(1L,pixels.right),std::max(1L,pixels.bottom));
  if(memory&&image){dc=memory;bitmap=image;previous=static_cast<HBITMAP>(SelectObject(dc,bitmap));}else{if(memory)DeleteDC(memory);if(image)DeleteObject(image);dc=target;}
  saved=SaveDC(dc);
 }
 ~SettingsPaintBuffer(){
  if(dc&&saved)RestoreDC(dc,saved);
  if(bitmap){BitBlt(target,0,0,pixels.right,pixels.bottom,dc,0,0,SRCCOPY);SelectObject(dc,previous);DeleteObject(bitmap);DeleteDC(dc);}
  EndPaint(window,&paint);
 }
 SettingsPaintBuffer(const SettingsPaintBuffer&)=delete;SettingsPaintBuffer& operator=(const SettingsPaintBuffer&)=delete;
};
void SettingsPaint(HWND window,const std::shared_ptr<NativeSettingsState>& state) {
 SettingsPaintBuffer buffer(window);auto dc=buffer.dc;if(!dc)return;FillRect(dc,&buffer.pixels,GetSysColorBrush(COLOR_WINDOW));SetMapMode(dc,MM_ANISOTROPIC);SetWindowExtEx(dc,96,96,nullptr);int dpi=GetDpiForWindow(window);SetViewportExtEx(dc,dpi,dpi,nullptr);
 auto font=SelectObject(dc,state->paintFont);SetBkMode(dc,TRANSPARENT);SetTextColor(dc,SettingsTextColor());
 RECT sidebar{0,0,184,state->contentHeight};auto side=CreateSolidBrush(g_highContrast?GetSysColor(COLOR_BTNFACE):RGB(245,246,248));FillRect(dc,&sidebar,side);DeleteObject(side);
 IntersectClipRect(dc,200,0,state->contentWidth,state->contentHeight);SetWindowOrgEx(dc,0,state->scroll,nullptr);
 if(state->linkPage){SettingsCard(dc,216,124,state->contentWidth-240,248);SettingsCard(dc,216,430,state->contentWidth-240,SettingsDocumentHeight(state)-454);}
 else {SettingsCard(dc,216,118,state->contentWidth-240,76);SettingsCard(dc,216,268,state->contentWidth-240,300);}
 if(state->linkPage) {
  RECT area{0,0,state->contentWidth,state->contentHeight+state->scroll};
  const auto status=state->status;const auto stage=JsonString(status,L"stage");
  const bool relay=JsonString(status,L"link")==L"fn-connect",upload=stage==L"upload",download=stage==L"download",latency=stage==L"latency";
  if(state->active) {
   RECT caption{240,514,area.right-72,578};
   auto text=GetText(state->notice);
   DrawTextW(dc,text.c_str(),static_cast<int>(text.size()),&caption,DT_CENTER|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS|DT_NOPREFIX);
   int x0=280,x1=(area.right+216)/2,x2=area.right-88,y=610;
   auto pen=CreatePen(PS_SOLID,2,RGB(30,125,235));auto previous=SelectObject(dc,pen);auto brush=SelectObject(dc,GetStockObject(NULL_BRUSH));
   auto node=[&](int x,int kind,const std::wstring& caption){
    RoundRect(dc,x-26,y-26,x+26,y+26,12,12);
    if(kind==0){Rectangle(dc,x-16,y-14,x+16,y+8);MoveToEx(dc,x,y+8,nullptr);LineTo(dc,x,y+17);MoveToEx(dc,x-13,y+17,nullptr);LineTo(dc,x+13,y+17);}
    else if(kind==1){RoundRect(dc,x-13,y-18,x+13,y+18,4,4);for(int row=0;row<3;row++){Ellipse(dc,x+6,y-13+row*12,x+9,y-10+row*12);if(row<2){MoveToEx(dc,x-13,y-6+row*12,nullptr);LineTo(dc,x+13,y-6+row*12);}}}
    else{POINT outline[]{{x-11,y-15},{x+11,y-15},{x+18,y+14},{x-18,y+14}};Polygon(dc,outline,4);MoveToEx(dc,x-15,y+5,nullptr);LineTo(dc,x+15,y+5);Ellipse(dc,x+9,y+8,x+12,y+11);}
    RECT title{x-70,y+32,x+70,y+65};auto color=SetTextColor(dc,GetSysColor(COLOR_GRAYTEXT));DrawTextW(dc,caption.c_str(),-1,&title,DT_CENTER|DT_WORDBREAK);SetTextColor(dc,color);
   };
   node(x0,0,L("当前设备"));if(relay)node(x1,1,L("FN Connect 中转"));node(x2,2,L"NAS");
   for(int segment=0;segment<(relay?2:1);segment++) {
    int a=segment==0?x0+31:x1+31,b=relay&&segment==0?x1-31:x2-31;
    for(int direction=0;direction<2;direction++) {
     bool highlighted=latency||(upload&&direction==0)||(download&&direction==1);
     auto lane=CreatePen(PS_SOLID,highlighted?2:1,highlighted?RGB(30,125,235):GetSysColor(COLOR_GRAYTEXT));SelectObject(dc,lane);
     int ly=y-7+direction*14;MoveToEx(dc,a,ly,nullptr);LineTo(dc,b,ly);int tip=direction?a:b,tail=direction?4:-4;MoveToEx(dc,tip+tail,ly-4,nullptr);LineTo(dc,tip,ly);LineTo(dc,tip+tail,ly+4);SelectObject(dc,pen);DeleteObject(lane);
    }
   }
   if(upload||download||latency) {
    const double seconds=GetTickCount64()/1000.0,cycle=std::fmod(seconds,1.0);
    for(int i=0;i<(latency?1:3);i++) {
     if(latency&&cycle>=.5)continue;
     bool back=download||(latency&&cycle>=.25);
     double position=latency?(1-std::cos(std::fmod(cycle,.25)/.25*3.141592653589793))/2:std::fmod(seconds*.65+i/3.0,1.0);
     if(back)position=1-position;
     int x=x0+31+static_cast<int>((x2-x0-62)*position),ly=y-7+(back?14:0);
     if(relay&&std::abs(x-x1)<31)continue;
     auto dot=CreateSolidBrush(RGB(30,125,235));SelectObject(dc,dot);Ellipse(dc,x-3,ly-3,x+3,ly+3);SelectObject(dc,GetStockObject(NULL_BRUSH));DeleteObject(dot);
    }
   }
   SelectObject(dc,brush);SelectObject(dc,previous);DeleteObject(pen);
   std::wstring metric;
   if(latency||upload||download){wchar_t buffer[96]{};swprintf_s(buffer,latency?L"%.1f ms":L"%.1f Mbps",SettingsNumber(status,latency?L"rttMs":L"mbps"));metric=buffer;}
   auto oldFont=SelectObject(dc,state->paintMetric);auto oldColor=SetTextColor(dc,RGB(30,125,235));RECT speed{240,700,area.right-48,740};DrawTextW(dc,metric.c_str(),-1,&speed,DT_CENTER);SetTextColor(dc,oldColor);SelectObject(dc,oldFont);
   double maximum=1;for(const auto& sample:state->samples)maximum=std::max(maximum,sample.rate);
   double origin=state->samples.empty()?0:std::max(0.0,state->samples.back().elapsed-state->sampleWindow);
   pen=CreatePen(PS_SOLID,2,RGB(30,125,235));previous=SelectObject(dc,pen);
   for(size_t i=0;i<state->samples.size();i++) {
    auto point=state->samples[i];if(point.elapsed<origin&&i+1<state->samples.size()){const auto next=state->samples[i+1];point.rate+=(next.rate-point.rate)*(origin-point.elapsed)/(next.elapsed-point.elapsed);point.elapsed=origin;}
    int x=240+static_cast<int>(std::clamp((point.elapsed-origin)/state->sampleWindow,0.0,1.0)*(area.right-288)),py=780-static_cast<int>(std::clamp(point.rate/maximum,0.0,1.0)*40);
    if(i==0)MoveToEx(dc,x,py,nullptr);else LineTo(dc,x,py);
   }
   SelectObject(dc,previous);DeleteObject(pen);
   pen=CreatePen(PS_SOLID,1,GetSysColor(COLOR_GRAYTEXT));previous=SelectObject(dc,pen);MoveToEx(dc,240,788,nullptr);LineTo(dc,area.right-48,788);SelectObject(dc,previous);DeleteObject(pen);
  }
  auto results=status.GetNamedArray(L"results",JsonArray{});int tableY=state->active?820:510;
  int colWidth=(area.right-288)/4;
  auto cell=[&](const std::wstring& text,int col,int y,int height,COLORREF color){RECT r{240+col*colWidth,y,240+(col+1)*colWidth-10,y+height};SetTextColor(dc,color);DrawTextW(dc,text.c_str(),-1,&r,DT_WORDBREAK|DT_NOPREFIX);};
  if(results.Size()){
   auto old=SelectObject(dc,state->paintHeading);int col=0;for(auto title:{L("网络链路"),L("延迟"),L("下载"),L("上传")})cell(title,col++,tableY,32,SettingsTextColor());SelectObject(dc,old);tableY+=36;
  }
  for(auto value:results){auto result=value.GetObject();cell(NativeLinkName(JsonString(result,L"link")),0,tableY,44,SettingsTextColor());int col=1;
   for(auto key:{L"latencyMs",L"downloadMbps",L"uploadMbps"}){std::wstring text=L"—";if(result.HasKey(key)){wchar_t number[64]{};swprintf_s(number,L"%.1f %s",SettingsNumber(result,key),wcscmp(key,L"latencyMs")==0?L"ms":L"Mbps");text=number;}cell(text,col++,tableY,44,SettingsTextColor());}
   auto message=JsonString(result,L"message");if(message.empty())message=JsonString(result,L"error");
   if(!message.empty()){RECT r{240,tableY+48,area.right-48,tableY+SettingsResultHeight(state,result)};SetTextColor(dc,JsonString(result,L"outcome")==L"unavailable"?GetSysColor(COLOR_GRAYTEXT):RGB(185,45,45));DrawTextW(dc,message.c_str(),-1,&r,DT_WORDBREAK|DT_NOPREFIX);}
   tableY+=SettingsResultHeight(state,result);
  }
  if(!state->failure.empty()){RECT error{240,tableY,area.right-96,tableY+SettingsMessageHeight(state,state->failure)};SetTextColor(dc,RGB(185,45,45));DrawTextW(dc,state->failure.c_str(),-1,&error,DT_WORDBREAK|DT_NOPREFIX);}
 }
 SelectObject(dc,font);
}

LRESULT CALLBACK SettingsChoicesProc(HWND window,UINT message,WPARAM wParam,LPARAM lParam){
 if(message==WM_COMMAND)return SendMessageW(GetWindow(window,GW_OWNER),message,wParam,lParam);
 // Focus can temporarily pass through the owner while a popup checkbox toggles.
 if(message==WM_ACTIVATE&&LOWORD(wParam)==WA_INACTIVE){PostMessageW(window,WM_APP+1,0,0);return 0;}
 if(message==WM_APP+1){auto focus=GetFocus();if(GetActiveWindow()!=window&&focus!=window&&!IsChild(window,focus))ShowWindow(window,SW_HIDE);return 0;}
 if(message==WM_CTLCOLORSTATIC||message==WM_CTLCOLORBTN){SetBkMode(reinterpret_cast<HDC>(wParam),TRANSPARENT);SetTextColor(reinterpret_cast<HDC>(wParam),SettingsTextColor());return reinterpret_cast<LRESULT>(GetSysColorBrush(COLOR_WINDOW));}
 return DefWindowProcW(window,message,wParam,lParam);
}
void SettingsShowChoices(const std::shared_ptr<NativeSettingsState>& state,bool show){
 state->choosing=show;if(!show){ShowWindow(state->choicesWindow,SW_HIDE);return;}
 RECT anchor{};GetWindowRect(state->selector,&anchor);int scale=GetDpiForWindow(state->window);int width=MulDiv(280,scale,96),height=MulDiv(144,scale,96);
 MONITORINFO monitor{sizeof(monitor)};GetMonitorInfoW(MonitorFromWindow(state->window,MONITOR_DEFAULTTONEAREST),&monitor);
 int x=std::clamp(anchor.left,monitor.rcWork.left,std::max(monitor.rcWork.left,monitor.rcWork.right-width)),y=anchor.bottom+SettingsScale(state->window,6);if(y+height>monitor.rcWork.bottom)y=anchor.top-height-SettingsScale(state->window,6);
 SetWindowPos(state->choicesWindow,HWND_TOP,x,y,width,height,SWP_SHOWWINDOW);
 for(int i=0;i<3;i++){auto control=state->choices[i];SetWindowPos(control,nullptr,SettingsScale(state->window,16),SettingsScale(state->window,12+i*40),SettingsScale(state->window,i==2?90:248),SettingsScale(state->window,32),SWP_NOZORDER);SendMessageW(control,WM_SETFONT,reinterpret_cast<WPARAM>(state->font),TRUE);}
 SetFocus(state->choices[0]);
}
LRESULT NativeSettingsDispatch(HWND window,UINT message,WPARAM wParam,LPARAM lParam){
 auto holder=reinterpret_cast<std::shared_ptr<NativeSettingsState>*>(GetWindowLongPtrW(window,GWLP_USERDATA));
 if(message==WM_NCCREATE){auto create=reinterpret_cast<CREATESTRUCTW*>(lParam);holder=new std::shared_ptr<NativeSettingsState>(*reinterpret_cast<std::shared_ptr<NativeSettingsState>*>(create->lpCreateParams));SetWindowLongPtrW(window,GWLP_USERDATA,reinterpret_cast<LONG_PTR>(holder));(*holder)->window=window;}
 if(!holder)return DefWindowProcW(window,message,wParam,lParam);auto state=*holder;
 switch(message){
 case WM_CREATE:{
  SettingsFonts(state);
  SettingsControl(window,L"BUTTON",L("通用"),BS_OWNERDRAW|WS_TABSTOP,kSettingsGeneral,16,28,152,42);
  SettingsControl(window,L"BUTTON",L("网络链路"),BS_OWNERDRAW|WS_TABSTOP,kSettingsLinks,16,78,152,42);
  state->pageTitle=SettingsControl(window,L"STATIC",L("通用"),SS_LEFT,0,216,28,250,40);
  state->contextLabel=SettingsControl(window,L"STATIC",L"",SS_RIGHT|SS_PATHELLIPSIS,0,490,42,398,24);state->links.push_back(state->contextLabel);
  state->startupTitle=SettingsControl(window,L"STATIC",L("启动"),SS_LEFT,0,216,82,600,30);state->general.push_back(state->startupTitle);
  state->autoLabel=SettingsControl(window,L"STATIC",L("开机自启"),SS_LEFT,0,240,142,540,28);state->general.push_back(state->autoLabel);
  state->general.push_back(SettingsControl(window,L"BUTTON",L("开机自启"),BS_OWNERDRAW|WS_TABSTOP,kSettingsAuto,840,132,48,44));
  state->aboutTitle=SettingsControl(window,L"STATIC",L("关于 FnCPN"),SS_LEFT,0,216,228,700,32);state->general.push_back(state->aboutTitle);
  state->brand=SettingsControl(window,L"STATIC",L"FnCPN",SS_LEFT,0,304,292,584,44);state->general.push_back(state->brand);
  state->buildLabel=SettingsControl(window,L"STATIC",FNCPN_PRODUCT_VERSION,SS_LEFT|SS_EDITCONTROL,0,240,350,640,24);state->general.push_back(state->buildLabel);
  state->commitLabel=SettingsControl(window,L"STATIC",L"Commit",SS_LEFT|SS_PATHELLIPSIS,0,240,384,600,28);state->general.push_back(state->commitLabel);
  state->general.push_back(SettingsControl(window,L"BUTTON",L("复制 Commit"),BS_OWNERDRAW|WS_TABSTOP,8112,780,380,28,28));
  state->iconView=SettingsControl(window,L"STATIC",L"FnCPN",SS_ICON|SS_CENTERIMAGE,0,240,290,48,48);state->general.push_back(state->iconView);
  state->appIcon=static_cast<HICON>(LoadImageW(GetModuleHandleW(nullptr),MAKEINTRESOURCEW(1),IMAGE_ICON,SettingsScale(window,48),SettingsScale(window,48),0));SendMessageW(state->iconView,STM_SETICON,reinterpret_cast<WPARAM>(state->appIcon),0);
  for(int i=0;i<3;i++)state->general.push_back(SettingsControl(window,L"BUTTON",i==0?L("GitHub 项目"):i==1?L("官方网站"):L("下载服务端与其他客户端"),BS_OWNERDRAW|WS_TABSTOP,8120+i,240,432+i*38,648,32));
  state->policyTitle=SettingsControl(window,L"STATIC",L("开关与优先级"),SS_LEFT,0,216,88,600,30);state->links.push_back(state->policyTitle);
  auto help=SettingsControl(window,L"BUTTON",L"?",BS_OWNERDRAW|WS_TABSTOP,8111,860,84,28,28);state->links.push_back(help);
  state->tooltip=CreateWindowExW(WS_EX_TOPMOST,TOOLTIPS_CLASSW,nullptr,WS_POPUP|TTS_ALWAYSTIP,CW_USEDEFAULT,CW_USEDEFAULT,CW_USEDEFAULT,CW_USEDEFAULT,window,nullptr,GetModuleHandleW(nullptr),nullptr);
  TOOLINFOW tool{sizeof(tool)};tool.uFlags=TTF_IDISHWND|TTF_SUBCLASS;tool.hwnd=window;tool.uId=reinterpret_cast<UINT_PTR>(help);state->helpText=L("打开或关闭开关可启用或禁用对应链路；拖动远程链路可调整优先级。连接时按从上到下的顺序尝试已启用的链路。局域网始终优先，且不可关闭或调整顺序。");
  for(auto delimiter:{std::wstring(L"；"),std::wstring(L"。"),std::wstring(L". ")}){size_t pos=0;while((pos=state->helpText.find(delimiter,pos))!=std::wstring::npos){pos+=delimiter.size();if(pos<state->helpText.size())state->helpText.insert(pos++,L"\n");}}
  tool.lpszText=state->helpText.data();SendMessageW(state->tooltip,TTM_ADDTOOLW,0,reinterpret_cast<LPARAM>(&tool));SendMessageW(state->tooltip,TTM_SETMAXTIPWIDTH,0,320);SendMessageW(state->tooltip,WM_SETFONT,reinterpret_cast<WPARAM>(state->font),TRUE);
  state->list=SettingsControl(window,WC_LISTVIEWW,L"",LVS_REPORT|LVS_SINGLESEL|LVS_NOCOLUMNHEADER|LVS_NOSCROLL|LVS_SHAREIMAGELISTS|WS_TABSTOP,kSettingsOrder,240,140,584,160);state->links.push_back(state->list);ListView_SetExtendedListViewStyle(state->list,LVS_EX_FULLROWSELECT|LVS_EX_DOUBLEBUFFER);LVCOLUMNW column{};column.mask=LVCF_WIDTH;column.cx=540;ListView_InsertColumn(state->list,0,&column);
  for(int i=0;i<3;i++){state->linkSwitches[i]=SettingsControl(window,L"BUTTON",L"",BS_OWNERDRAW|WS_TABSTOP,8140+i,840,144+i*52,48,44);state->links.push_back(state->linkSwitches[i]);}
  state->links.push_back(SettingsControl(window,L"BUTTON",L("恢复默认设置"),BS_OWNERDRAW|WS_TABSTOP,kSettingsReset,240,316,180,36));
  state->testTitle=SettingsControl(window,L"STATIC",L("网络链路测试"),SS_LEFT,0,216,394,600,30);state->links.push_back(state->testTitle);
  state->links.push_back(SettingsControl(window,L"STATIC",L("测试链路"),SS_CENTERIMAGE,0,240,450,90,36));
  state->selector=SettingsControl(window,L"BUTTON",L("IPv6 与 FN Connect"),BS_OWNERDRAW|WS_TABSTOP,kSettingsChoose,340,450,260,36);state->links.push_back(state->selector);
  state->start=SettingsControl(window,L"BUTTON",L("开始测试"),BS_OWNERDRAW|WS_TABSTOP,kSettingsStart,616,450,130,36);state->links.push_back(state->start);
  state->cancelButton=SettingsControl(window,L"BUTTON",L("取消"),BS_OWNERDRAW|WS_TABSTOP,kSettingsCancel,616,450,130,36);state->links.push_back(state->cancelButton);
  state->notice=SettingsControl(window,L"STATIC",L"",SS_CENTER|SS_EDITCONTROL,0,240,514,648,64);state->links.push_back(state->notice);
  state->links.push_back(SettingsControl(window,L"BUTTON",L"×",BS_OWNERDRAW|WS_TABSTOP,8113,860,514,28,28));
  WNDCLASSW popup{};popup.lpfnWndProc=SettingsChoicesProc;popup.hInstance=GetModuleHandleW(nullptr);popup.lpszClassName=L"FnCPN.SettingsChoices";popup.hCursor=LoadCursorW(nullptr,IDC_ARROW);popup.hbrBackground=GetSysColorBrush(COLOR_WINDOW);RegisterClassW(&popup);
  state->choicesWindow=CreateWindowExW(WS_EX_TOOLWINDOW,popup.lpszClassName,L("测试链路"),WS_POPUP|WS_BORDER,0,0,280,144,window,nullptr,popup.hInstance,nullptr);
  state->choices.push_back(SettingsControl(state->choicesWindow,L"BUTTON",L("IPv6 直连"),BS_AUTOCHECKBOX|WS_TABSTOP,kSettingsIPv6,16,12,248,32));state->choices.push_back(SettingsControl(state->choicesWindow,L"BUTTON",L("FN Connect 中继"),BS_AUTOCHECKBOX|WS_TABSTOP,kSettingsRelay,16,52,248,32));state->choices.push_back(SettingsControl(state->choicesWindow,L"BUTTON",L("完成"),BS_PUSHBUTTON|WS_TABSTOP,kSettingsDone,16,92,90,32));for(int i=0;i<2;i++)SendMessageW(state->choices[i],BM_SETCHECK,BST_CHECKED,0);
  SettingsFonts(state);SettingsRows(state);
  SettingsPage(state,false);SetTimer(window,1,33,nullptr);
#ifndef FNCPN_NATIVE_TEST
  StartWorker([state]{try{SettingsWorkerApartment apartment;auto settings=CallIpc(L"link-settings").GetNamedObject(L"result");auto build=CallIpc(L"build-info").GetNamedObject(L"result");std::lock_guard lock(state->mutex);state->pendingPreferences=settings.Stringify().c_str();state->pendingCommit=JsonString(build,L"commit");state->pendingBuildInfo=std::wstring(L("版本"))+L" "+JsonString(build,L"version");}catch(const winrt::hresult_error&e){std::lock_guard lock(state->mutex);state->pendingFailure=e.message().c_str();state->pendingFailureSet=true;}catch(const std::exception&){std::lock_guard lock(state->mutex);state->pendingFailure=L("设置页面更新失败，请关闭后重试。");state->pendingFailureSet=true;}SettingsNotify(state);});
#endif
  return 0;
 }
 case WM_COMMAND:{int command=LOWORD(wParam);
  if(command==kSettingsGeneral||command==kSettingsLinks){SettingsPage(state,command==kSettingsLinks);return 0;}
  if(command==8113){state->failure.clear();SettingsLayout(state);return 0;}
  if(command==8112){CopyText(window,state->commit);SetWindowTextW(GetDlgItem(window,8112),L("已复制 Commit"));return 0;}
  if(command==kSettingsAuto){SetAutoStart(!g_autoStart);InvalidateRect(reinterpret_cast<HWND>(lParam),nullptr,TRUE);return 0;}
  if(command>=8120&&command<=8122){auto opened=ShellExecuteW(window,L"open",command==8120?L"https://github.com/rectcircle/fn-connect-private-network":command==8121?L"https://fncpn.rectcircle.cn/":L"https://github.com/rectcircle/fn-connect-private-network/releases",nullptr,nullptr,SW_SHOWNORMAL);if(reinterpret_cast<INT_PTR>(opened)<=32)MessageBoxW(window,L("无法打开链接，请检查浏览器设置。"),L("设置"),MB_OK|MB_ICONERROR);return 0;}
  if(command==kSettingsChoose||command==kSettingsDone){if(state->active)return 0;SettingsShowChoices(state,command!=kSettingsDone&&!IsWindowVisible(state->choicesWindow));return 0;}
  if(command>=8141&&command<=8142){if(state->saving)return 0;auto links=state->preferences.GetNamedArray(L"links",JsonArray{});if(links.Size()==2){auto item=links.GetObjectAt(command-8141);item.Insert(L"enabled",JsonValue::CreateBooleanValue(!JsonBool(item,L"enabled")));SettingsRows(state);SettingsSave(state);}return 0;}
  if(command==kSettingsIPv6||command==kSettingsRelay){bool ipv6=SendMessageW(state->choices[0],BM_GETCHECK,0,0)==BST_CHECKED,relay=SendMessageW(state->choices[1],BM_GETCHECK,0,0)==BST_CHECKED;SetWindowTextW(state->selector,ipv6&&relay?L("IPv6 与 FN Connect"):ipv6?L("IPv6 直连"):relay?L("FN Connect 中继"):L("选择测试链路"));return 0;}
  if(command==kSettingsCancel){SetEvent(state->cancellation);return 0;}
  if(command==kSettingsReset){if(state->saving||JsonString(state->preferences,L"fnId").empty())return 0;JsonArray links;for(auto key:{L"ipv6",L"fn-connect"}){JsonObject link;link.Insert(L"link",JsonValue::CreateStringValue(key));link.Insert(L"enabled",JsonValue::CreateBooleanValue(true));links.Append(link.as<IJsonValue>());}state->preferences.Insert(L"links",links.as<IJsonValue>());SettingsRows(state);SettingsSave(state);return 0;}
  if(command==kSettingsStart&&!state->active&&!state->workerRunning){bool ipv6=SendMessageW(state->choices[0],BM_GETCHECK,0,0)==BST_CHECKED,relay=SendMessageW(state->choices[1],BM_GETCHECK,0,0)==BST_CHECKED;if(!ipv6&&!relay)return 0;
   if(MessageBoxW(window,L("测试期间会重新建立连接，当前网络访问可能短暂中断。\n\n测试结果仅供参考，受当前设备与 NAS 的网络状况、可用带宽及 FN Connect 中转服务器带宽限制。"),L("开始网络链路测试？"),MB_OKCANCEL|MB_ICONINFORMATION)!=IDOK)return 0;
   {state->failure.clear();state->status=JsonObject{};state->samples.clear();}state->active=true;state->workerRunning=true;ResetEvent(state->cancellation);EnableWindow(state->selector,FALSE);EnableWindow(state->start,FALSE);EnableWindow(state->cancelButton,TRUE);SettingsShowChoices(state,false);SettingsLayout(state,true);std::wstring link=ipv6&&relay?L"all":ipv6?L"ipv6":L"fn-connect";
   StartWorker([state,link]{try{SettingsWorkerApartment apartment;JsonObject input;input.Insert(L"link",JsonValue::CreateStringValue(link));CallIpc(L"speedtest",&input,[state](const JsonObject& value){{std::lock_guard lock(state->mutex);state->pendingStatus=value.Stringify().c_str();}SettingsNotify(state);return !JsonBool(value,L"active");},state->cancellation);}catch(const winrt::hresult_error&e){std::lock_guard lock(state->mutex);if(WaitForSingleObject(state->cancellation,0)!=WAIT_OBJECT_0){state->pendingFailure=e.message().c_str();state->pendingFailureSet=true;}}catch(const std::exception&){std::lock_guard lock(state->mutex);state->pendingFailure=L("设置页面更新失败，请关闭后重试。");state->pendingFailureSet=true;}{std::lock_guard lock(state->mutex);state->pendingFinished=true;}SettingsNotify(state);});return 0;
  }break;
 }
 case WM_DRAWITEM:{auto item=reinterpret_cast<DRAWITEMSTRUCT*>(lParam);auto dc=item->hDC;int id=item->CtlID;auto r=item->rcItem;
  if(id==kSettingsGeneral||id==kSettingsLinks){bool selected=state->linkPage==(id==kSettingsLinks);COLORREF color=g_highContrast?GetSysColor(selected?COLOR_HIGHLIGHT:COLOR_BTNFACE):selected?RGB(220,234,251):RGB(245,246,248);auto brush=CreateSolidBrush(color);auto rail=CreateSolidBrush(g_highContrast?GetSysColor(COLOR_BTNFACE):RGB(245,246,248));FillRect(dc,&r,rail);DeleteObject(rail);if(selected){auto oldBrush=SelectObject(dc,brush);auto oldPen=SelectObject(dc,GetStockObject(NULL_PEN));int radius=SettingsScale(window,10);RoundRect(dc,r.left,r.top,r.right,r.bottom,radius,radius);SelectObject(dc,oldBrush);SelectObject(dc,oldPen);}DeleteObject(brush);auto font=SelectObject(dc,state->font);SetBkMode(dc,TRANSPARENT);SetTextColor(dc,selected?(g_highContrast?GetSysColor(COLOR_HIGHLIGHTTEXT):RGB(0,100,190)):SettingsTextColor());r.left+=SettingsScale(window,16);DrawTextW(dc,GetText(item->hwndItem).c_str(),-1,&r,DT_SINGLELINE|DT_VCENTER|DT_NOPREFIX);SelectObject(dc,font);if(item->itemState&ODS_FOCUS)DrawFocusRect(dc,&item->rcItem);return TRUE;}
  if(id==8112||(id>=8120&&id<=8122)){
   FillRect(dc,&r,GetSysColorBrush(COLOR_WINDOW));SetBkMode(dc,TRANSPARENT);auto font=SelectObject(dc,state->font);auto scale=[&](int n){return SettingsScale(window,n);};
   if(id==8112){auto pen=CreatePen(PS_SOLID,scale(1),GetSysColor(COLOR_GRAYTEXT));auto oldPen=SelectObject(dc,pen);auto oldBrush=SelectObject(dc,GetStockObject(NULL_BRUSH));RoundRect(dc,scale(7),scale(6),scale(17),scale(19),scale(2),scale(2));RoundRect(dc,scale(11),scale(10),scale(21),scale(23),scale(2),scale(2));SelectObject(dc,oldBrush);SelectObject(dc,oldPen);DeleteObject(pen);}
   else{SetTextColor(dc,g_highContrast?GetSysColor(COLOR_HOTLIGHT):RGB(0,103,192));auto text=GetText(item->hwndItem)+L" ↗";DrawTextW(dc,text.c_str(),-1,&r,DT_SINGLELINE|DT_VCENTER|DT_NOPREFIX);}
   SelectObject(dc,font);if(item->itemState&ODS_FOCUS)DrawFocusRect(dc,&r);return TRUE;
  }
  if(id==kSettingsReset||id==kSettingsChoose||id==kSettingsStart||id==kSettingsCancel||id==8111||id==8112||id==8113||(id>=8120&&id<=8122)){
   bool primary=id==kSettingsStart,disabled=(item->itemState&ODS_DISABLED)!=0;
   auto scale=[&](int n){return SettingsScale(window,n);};COLORREF bg=g_highContrast?GetSysColor(primary?COLOR_HIGHLIGHT:COLOR_BTNFACE):primary?RGB(0,120,215):RGB(239,241,245);
   if(item->itemState&ODS_SELECTED)bg=g_highContrast?GetSysColor(COLOR_HIGHLIGHT):RGB(213,226,242);
   auto brush=CreateSolidBrush(bg);auto oldBrush=SelectObject(dc,brush);auto oldPen=SelectObject(dc,GetStockObject(NULL_PEN));FillRect(dc,&r,GetSysColorBrush(COLOR_WINDOW));RoundRect(dc,0,0,r.right,r.bottom,scale(id==8111?28:8),scale(id==8111?28:8));SelectObject(dc,oldBrush);SelectObject(dc,oldPen);DeleteObject(brush);
   auto font=SelectObject(dc,state->font);SetBkMode(dc,TRANSPARENT);SetTextColor(dc,disabled?GetSysColor(COLOR_GRAYTEXT):primary?GetSysColor(COLOR_HIGHLIGHTTEXT):SettingsTextColor());RECT text=r;if(id==kSettingsChoose)text.right-=scale(24);DrawTextW(dc,GetText(item->hwndItem).c_str(),-1,&text,DT_SINGLELINE|DT_VCENTER|DT_CENTER|DT_NOPREFIX);SelectObject(dc,font);
   if(id==kSettingsChoose){auto pen=CreatePen(PS_SOLID,scale(1),disabled?GetSysColor(COLOR_GRAYTEXT):SettingsTextColor());auto old=SelectObject(dc,pen);int x=r.right-scale(17),y=r.bottom/2;MoveToEx(dc,x-scale(4),y-scale(2),nullptr);LineTo(dc,x,y+scale(2));LineTo(dc,x+scale(4),y-scale(2));SelectObject(dc,old);DeleteObject(pen);}
   if(item->itemState&ODS_FOCUS){InflateRect(&r,-scale(3),-scale(3));DrawFocusRect(dc,&r);}return TRUE;
  }
  if(id==kSettingsAuto||(id>=8140&&id<=8142)){int index=id-8140;auto links=state->preferences.GetNamedArray(L"links",JsonArray{});bool on=id==kSettingsAuto?g_autoStart:index==0||(links.Size()==2&&JsonBool(links.GetObjectAt(index-1),L"enabled"));FillRect(dc,&r,GetSysColorBrush(COLOR_WINDOW));auto scale=[&](int n){return SettingsScale(window,n);};int y=(r.bottom-scale(22))/2;auto track=CreateSolidBrush(!IsWindowEnabled(item->hwndItem)?RGB(165,190,220):on?RGB(0,120,215):GetSysColor(COLOR_GRAYTEXT));auto oldBrush=SelectObject(dc,track);auto oldPen=SelectObject(dc,GetStockObject(NULL_PEN));RoundRect(dc,scale(4),y,scale(44),y+scale(22),scale(22),scale(22));SelectObject(dc,GetStockObject(WHITE_BRUSH));int x=scale(on?24:6);Ellipse(dc,x,y+scale(2),x+scale(18),y+scale(20));SelectObject(dc,oldBrush);SelectObject(dc,oldPen);DeleteObject(track);if(item->itemState&ODS_FOCUS)DrawFocusRect(dc,&r);return TRUE;}break;}
 case WM_NOTIFY:{auto header=reinterpret_cast<NMHDR*>(lParam);if(header->idFrom==kSettingsOrder){if(header->code==NM_CUSTOMDRAW){auto draw=reinterpret_cast<NMLVCUSTOMDRAW*>(lParam);if(draw->nmcd.dwDrawStage==CDDS_PREPAINT)return CDRF_NOTIFYITEMDRAW;if(draw->nmcd.dwDrawStage==CDDS_ITEMPREPAINT&&draw->nmcd.dwItemSpec==0){draw->clrText=GetSysColor(COLOR_GRAYTEXT);return CDRF_NEWFONT;}}if(state->renderingRows)return 0;  if(header->code==LVN_BEGINDRAG){auto drag=reinterpret_cast<NMLISTVIEW*>(lParam);if(drag->iItem>0&&!state->saving&&!JsonString(state->preferences,L"fnId").empty()){SettingsBeginDrag(state,drag->iItem);}}}break;}
 case WM_MOUSEMOVE:if(state->dragging>0){POINT cursor{};GetCursorPos(&cursor);ImageList_DragMove(cursor.x,cursor.y);ScreenToClient(state->list,&cursor);LVHITTESTINFO hit{};hit.pt=cursor;int row=ListView_HitTest(state->list,&hit);ImageList_DragShowNolock(FALSE);ListView_SetItemState(state->list,-1,0,LVIS_DROPHILITED);if(row>0)ListView_SetItemState(state->list,row,LVIS_DROPHILITED,LVIS_DROPHILITED);ImageList_DragShowNolock(TRUE);SetCursor(LoadCursorW(nullptr,IDC_SIZEALL));return 0;}break;
 case WM_CAPTURECHANGED:SettingsEndDrag(state);return 0;
 case WM_LBUTTONUP:if(state->dragging>0){POINT cursor{};GetCursorPos(&cursor);ScreenToClient(state->list,&cursor);LVHITTESTINFO hit{};hit.pt=cursor;int row=ListView_HitTest(state->list,&hit);int source=state->dragging;SettingsEndDrag(state);ReleaseCapture();if(row>0&&row!=source){auto links=state->preferences.GetNamedArray(L"links");auto first=links.GetAt(0);links.SetAt(0,links.GetAt(1));links.SetAt(1,first);SettingsRows(state);SettingsSave(state);}state->dragging=-1;}return 0;
 case kSettingsUpdate:if(wParam==state->instanceId){
 std::wstring preferences,status,failure,buildInfo,commit;bool failureSet=false,finished=false,saved=false;
 {std::lock_guard lock(state->mutex);preferences.swap(state->pendingPreferences);status.swap(state->pendingStatus);failure.swap(state->pendingFailure);buildInfo.swap(state->pendingBuildInfo);commit.swap(state->pendingCommit);failureSet=state->pendingFailureSet;state->pendingFailureSet=false;finished=state->pendingFinished;state->pendingFinished=false;saved=state->saveCompleted;state->saveCompleted=false;}
 // Release the mailbox lock before any synchronous window messages/repaint.
 if(!preferences.empty()){state->preferences=JsonObject::Parse(preferences);state->saving=false;SettingsRows(state);}
 if(!status.empty()){state->status=JsonObject::Parse(status);state->active=JsonBool(state->status,L"active");}
 if(finished){state->active=false;state->workerRunning=false;}
 if(failureSet)state->failure=std::move(failure);
 if(!buildInfo.empty())state->buildInfo=std::move(buildInfo);
 if(!commit.empty())state->commit=std::move(commit);
 if(saved){state->saving=false;SettingsRows(state);}
 EnableWindow(GetDlgItem(window,kSettingsReset),!state->saving&&!JsonString(state->preferences,L"fnId").empty());
 if(!state->buildInfo.empty()){if(GetText(state->buildLabel)!=state->buildInfo)SetWindowTextW(state->buildLabel,state->buildInfo.c_str());auto revision=L"Commit "+state->commit;if(GetText(state->commitLabel)!=revision)SetWindowTextW(state->commitLabel,revision.c_str());}auto stage=JsonString(state->status,L"stage");auto family=stage.starts_with(L"upload")?std::wstring(L"upload"):stage;auto link=JsonString(state->status,L"link");
 if(link!=state->sampleLink||((family==L"upload"||family==L"download")&&family!=state->sampleStage))state->samples.clear();state->sampleLink=link;
 if(family==L"upload"||family==L"download"){
  state->sampleStage=family;state->sampleWindow=std::max(1.0,SettingsNumber(state->status,L"targetSampleMs",8000));auto elapsed=SettingsNumber(state->status,L"sampleElapsedMs");
  if(elapsed>0&&(state->samples.empty()||elapsed>state->samples.back().elapsed)){state->samples.push_back({elapsed,SettingsNumber(state->status,L"mbps")});auto cutoff=std::max(0.0,elapsed-state->sampleWindow);while(state->samples.size()>1&&state->samples[1].elapsed<=cutoff)state->samples.erase(state->samples.begin());}
 }
 std::wstring phase=stage;for(auto pair:std::initializer_list<std::pair<const wchar_t*,const wchar_t*>>{{L"preparing",L("准备测试")},{L"checking",L("检查链路条件")},{L"verifying",L("正在验证 NAS")},{L"upload-warmup",L("正在预热上传")},{L"upload-waiting",L("等待 NAS 接收数据")},{L"upload-confirming",L("等待 NAS 完成确认")},{L"upload-complete",L("上传完成")},{L"connecting",L("正在建立真实链路")},{L"latency",L("正在测试延迟")},{L"download",L("正在测试下载")},{L"upload",L("正在测试上传")},{L"restoring",L("正在恢复连接")},{L"completed",L("测试完成")},{L"canceled",L("已取消")}}){if(stage==pair.first)phase=pair.second;}
 if(!link.empty()&&stage!=L"restoring")phase=NativeLinkName(link)+L" · "+phase;
 SettingsContext(state);if(GetText(state->notice)!=phase)SetWindowTextW(state->notice,phase.c_str());EnableWindow(state->selector,!state->active);EnableWindow(state->start,!state->active&&!state->workerRunning);EnableWindow(state->cancelButton,state->active);SettingsVisible(state->start,state->linkPage&&!state->active);SettingsVisible(state->cancelButton,state->linkPage&&state->active);SettingsLayout(state,state->linkPage&&(state->active||JsonString(state->status,L"stage")==L"completed"));InvalidateRect(window,nullptr,FALSE);}return 0;
 case WM_SIZE:SettingsShowChoices(state,false);SettingsLayout(state);return 0;
 case WM_GETMINMAXINFO:{auto info=reinterpret_cast<MINMAXINFO*>(lParam);info->ptMinTrackSize={SettingsScale(window,900),SettingsScale(window,580)};return 0;}
 case WM_DPICHANGED:{auto rect=reinterpret_cast<RECT*>(lParam);SettingsFonts(state);SetWindowPos(window,nullptr,rect->left,rect->top,rect->right-rect->left,rect->bottom-rect->top,SWP_NOZORDER|SWP_NOACTIVATE);SettingsLayout(state);return 0;}
 case WM_MOUSEWHEEL:state->scroll-=GET_WHEEL_DELTA_WPARAM(wParam)/WHEEL_DELTA*52;SettingsShowChoices(state,false);SettingsLayout(state);return 0;
 case WM_VSCROLL:{SCROLLINFO info{sizeof(info),SIF_TRACKPOS};GetScrollInfo(window,SB_VERT,&info);switch(LOWORD(wParam)){case SB_LINEUP:state->scroll-=32;break;case SB_LINEDOWN:state->scroll+=32;break;case SB_PAGEUP:state->scroll-=state->contentHeight-40;break;case SB_PAGEDOWN:state->scroll+=state->contentHeight-40;break;case SB_THUMBTRACK:state->scroll=info.nTrackPos;break;}SettingsShowChoices(state,false);SettingsLayout(state);return 0;}
 case WM_SETCURSOR:{auto control=reinterpret_cast<HWND>(wParam);wchar_t name[32]{};GetClassNameW(control,name,32);if(LOWORD(lParam)==HTCLIENT&&control==state->list){POINT cursor{};GetCursorPos(&cursor);ScreenToClient(state->list,&cursor);LVHITTESTINFO hit{};hit.pt=cursor;if(ListView_HitTest(state->list,&hit)>0){SetCursor(LoadCursorW(nullptr,IDC_HAND));return TRUE;}}if(LOWORD(lParam)==HTCLIENT&&IsWindowEnabled(control)&&_wcsicmp(name,L"Button")==0){SetCursor(LoadCursorW(nullptr,IDC_HAND));return TRUE;}break;}
 case WM_CTLCOLORSTATIC:case WM_CTLCOLORBTN:{auto dc=reinterpret_cast<HDC>(wParam);SetBkMode(dc,TRANSPARENT);SetBkColor(dc,GetSysColor(COLOR_WINDOW));auto control=reinterpret_cast<HWND>(lParam);SetTextColor(dc,control==state->notice&&!state->failure.empty()?RGB(185,45,45):(control==state->buildLabel||control==state->commitLabel)?GetSysColor(COLOR_GRAYTEXT):SettingsTextColor());return reinterpret_cast<LRESULT>(GetSysColorBrush(COLOR_WINDOW));}
 case WM_ERASEBKGND:return 1;
 case WM_TIMER:if(state->active){RECT region{SettingsScale(window,216),SettingsScale(window,std::max(0,578-state->scroll)),SettingsScale(window,state->contentWidth),SettingsScale(window,state->contentHeight)};InvalidateRect(window,&region,FALSE);}return 0;
 case WM_PAINT:SettingsPaint(window,state);return 0;
 case WM_DESTROY:SettingsEndDrag(state);if(IsWindow(state->choicesWindow))DestroyWindow(state->choicesWindow);return 0;
 case WM_CLOSE:SettingsEndDrag(state);DestroyWindow(state->choicesWindow);DestroyWindow(window);return 0;
 case WM_NCDESTROY:{SetEvent(state->cancellation);{std::lock_guard lock(state->mutex);state->window=nullptr;}state->status=nullptr;state->preferences=nullptr;g_nativeSettings=nullptr;SetWindowLongPtrW(window,GWLP_USERDATA,0);delete holder;return DefWindowProcW(window,message,wParam,lParam);}
 }
 return DefWindowProcW(window,message,wParam,lParam);
}
LRESULT CALLBACK NativeSettingsProc(HWND window,UINT message,WPARAM wParam,LPARAM lParam){
 try{return NativeSettingsDispatch(window,message,wParam,lParam);}
 catch(const winrt::hresult_error& error){
  auto holder=reinterpret_cast<std::shared_ptr<NativeSettingsState>*>(GetWindowLongPtrW(window,GWLP_USERDATA));
  if(holder){auto state=*holder;SetEvent(state->cancellation);state->active=false;state->status=JsonObject{};state->failure=error.message().c_str();InvalidateRect(window,nullptr,FALSE);}
  return 0;
 }
 catch(const std::exception&){
  auto holder=reinterpret_cast<std::shared_ptr<NativeSettingsState>*>(GetWindowLongPtrW(window,GWLP_USERDATA));
  if(holder){auto state=*holder;SetEvent(state->cancellation);state->active=false;state->status=JsonObject{};state->failure=L("设置页面更新失败，请关闭后重试。");InvalidateRect(window,nullptr,FALSE);}
  return 0;
 }
}
void OpenNativeSettings(){if(g_nativeSettings){ShowWindow(g_nativeSettings,SW_SHOW);SetForegroundWindow(g_nativeSettings);return;}WNDCLASSW klass{};klass.lpfnWndProc=NativeSettingsProc;klass.hInstance=GetModuleHandleW(nullptr);klass.lpszClassName=L"FnCPN.NativeSettings";klass.hCursor=LoadCursorW(nullptr,IDC_ARROW);klass.hIcon=LoadIconW(klass.hInstance,MAKEINTRESOURCEW(1));klass.hbrBackground=reinterpret_cast<HBRUSH>(COLOR_WINDOW+1);RegisterClassW(&klass);auto state=std::make_shared<NativeSettingsState>();g_nativeSettings=CreateWindowExW(WS_EX_APPWINDOW|WS_EX_COMPOSITED,klass.lpszClassName,L("设置"),WS_OVERLAPPEDWINDOW|WS_CLIPCHILDREN|WS_VSCROLL,CW_USEDEFAULT,CW_USEDEFAULT,SettingsScale(g_window,1000),SettingsScale(g_window,820),g_window,nullptr,klass.hInstance,&state);if(!g_nativeSettings)return;ShowWindow(g_nativeSettings,SW_SHOW);}
