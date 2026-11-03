// Native settings share the Win32 control and IPC stack with the main window.
constexpr UINT kSettingsUpdate = WM_APP + 80;
constexpr int kSettingsGeneral=8100,kSettingsLinks=8101,kSettingsAuto=8102,kSettingsOrder=8103,kSettingsReset=8104,kSettingsChoose=8105,kSettingsIPv6=8106,kSettingsRelay=8107,kSettingsStart=8108,kSettingsCancel=8109,kSettingsDone=8110;
struct NativeThroughputPoint { double elapsed; double rate; };
struct NativeSettingsState {
 HWND window{},list{},notice{},selector{},start{},cancelButton{};
 HWND linkSwitches[3]{};
 std::vector<HWND> general,links,choices;
 std::mutex mutex;
 JsonObject preferences{},status{};
 std::wstring failure;
 std::wstring buildInfo;
 std::wstring commit;
 HWND buildLabel{};
 std::atomic<bool> active{false};
 bool choosing=false, linkPage=false, renderingRows=false;
 std::wstring pendingPreferences;
 int dragging=-1;
 HANDLE cancellation=CreateEventW(nullptr,TRUE,FALSE,nullptr);
 std::vector<NativeThroughputPoint> samples;
 double sampleWindow=8000;
 std::wstring sampleLink;
 std::wstring sampleStage;
 ~NativeSettingsState(){CloseHandle(cancellation);}
};
HWND g_nativeSettings{};
std::wstring NativeLinkName(const std::wstring& link){return link==L"ipv6"?L("IPv6 直连"):L("FN Connect 中继");}
HWND SettingsControl(HWND parent,const wchar_t* klass,const wchar_t* text,DWORD style,int id,int x,int y,int w,int h){
 auto child=CreateWindowExW(0,klass,text,WS_CHILD|WS_VISIBLE|style,x,y,w,h,parent,reinterpret_cast<HMENU>(static_cast<INT_PTR>(id)),GetModuleHandleW(nullptr),nullptr);
 SendMessageW(child,WM_SETFONT,reinterpret_cast<WPARAM>(g_font),TRUE);return child;
}
void SettingsNotify(const std::shared_ptr<NativeSettingsState>& state){std::lock_guard lock(state->mutex);if(state->window)PostMessageW(state->window,kSettingsUpdate,reinterpret_cast<WPARAM>(state.get()),0);}
void SettingsSave(const std::shared_ptr<NativeSettingsState>& state){
 JsonObject preferences;{std::lock_guard lock(state->mutex);preferences=JsonObject::Parse(state->preferences.Stringify());}
 StartWorker([state,preferences]{winrt::init_apartment(winrt::apartment_type::multi_threaded);try{auto saved=CallIpc(L"set-link-settings",&preferences).GetNamedObject(L"result");std::lock_guard lock(state->mutex);state->pendingPreferences=saved.Stringify().c_str();state->failure.clear();}catch(const winrt::hresult_error& e){std::lock_guard lock(state->mutex);state->failure=e.message().c_str();}SettingsNotify(state);winrt::uninit_apartment();});
}
void SettingsRows(const std::shared_ptr<NativeSettingsState>& state){
 state->renderingRows=true;
 ListView_DeleteAllItems(state->list);auto links=state->preferences.GetNamedArray(L"links",JsonArray{});
 for(int i=0;i<3;i++){std::wstring label=i==0?L("局域网 · 始终优先"):NativeLinkName(JsonString(links.GetObjectAt(i-1),L"link"));LVITEMW row{};row.mask=LVIF_TEXT;row.iItem=i;row.pszText=label.data();SetWindowTextW(state->linkSwitches[i],label.c_str());ListView_InsertItem(state->list,&row);RECT bounds{};ListView_GetItemRect(state->list,i,&bounds,LVIR_BOUNDS);MoveWindow(state->linkSwitches[i],640,50+bounds.top,48,bounds.bottom-bounds.top,TRUE);EnableWindow(state->linkSwitches[i],i>0);InvalidateRect(state->linkSwitches[i],nullptr,TRUE);}
 state->renderingRows=false;
}
void SettingsPage(const std::shared_ptr<NativeSettingsState>& state,bool links){state->linkPage=links;SendMessageW(GetDlgItem(state->window,kSettingsGeneral),BM_SETCHECK,links?BST_UNCHECKED:BST_CHECKED,0);SendMessageW(GetDlgItem(state->window,kSettingsLinks),BM_SETCHECK,links?BST_CHECKED:BST_UNCHECKED,0);for(auto control:state->general)ShowWindow(control,links?SW_HIDE:SW_SHOW);for(auto control:state->links)ShowWindow(control,links?SW_SHOW:SW_HIDE);for(auto control:state->choices)ShowWindow(control,SW_HIDE);state->choosing=false;ShowWindow(state->start,links&&!state->active?SW_SHOW:SW_HIDE);ShowWindow(state->cancelButton,links&&state->active?SW_SHOW:SW_HIDE);InvalidateRect(state->window,nullptr,TRUE);}
void SettingsPaint(HWND window,const std::shared_ptr<NativeSettingsState>& state) {
 PAINTSTRUCT paint{};auto dc=BeginPaint(window,&paint);auto font=SelectObject(dc,g_font);SetBkMode(dc,TRANSPARENT);
 if(state->linkPage) {
  RECT area{};GetClientRect(window,&area);
  const auto status=state->status;const auto stage=JsonString(status,L"stage");
  const bool relay=JsonString(status,L"link")==L"fn-connect",upload=stage==L"upload",download=stage==L"download",latency=stage==L"latency";
  if(state->active) {
   int x0=250,x1=(area.right+220)/2,x2=area.right-60,y=385;
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
   if(latency||upload||download){wchar_t buffer[96]{};swprintf_s(buffer,latency?L"%.1f ms":L"%.1f Mbps",status.GetNamedNumber(latency?L"rttMs":L"mbps",0));metric=buffer;}
   auto oldFont=SelectObject(dc,g_statusFont);auto oldColor=SetTextColor(dc,RGB(30,125,235));RECT speed{220,465,area.right-30,505};DrawTextW(dc,metric.c_str(),-1,&speed,DT_CENTER);SetTextColor(dc,oldColor);SelectObject(dc,oldFont);
   double maximum=1;for(const auto& sample:state->samples)maximum=std::max(maximum,sample.rate);
   double origin=state->samples.empty()?0:std::max(0.0,state->samples.back().elapsed-state->sampleWindow);
   pen=CreatePen(PS_SOLID,2,RGB(30,125,235));previous=SelectObject(dc,pen);
   for(size_t i=0;i<state->samples.size();i++) {
    auto point=state->samples[i];if(point.elapsed<origin&&i+1<state->samples.size()){const auto next=state->samples[i+1];point.rate+=(next.rate-point.rate)*(origin-point.elapsed)/(next.elapsed-point.elapsed);point.elapsed=origin;}
    int x=220+static_cast<int>((point.elapsed-origin)/state->sampleWindow*(area.right-260)),py=555-static_cast<int>(point.rate/maximum*40);
    if(i==0)MoveToEx(dc,x,py,nullptr);else LineTo(dc,x,py);
   }
   SelectObject(dc,previous);DeleteObject(pen);
   pen=CreatePen(PS_SOLID,1,GetSysColor(COLOR_GRAYTEXT));previous=SelectObject(dc,pen);MoveToEx(dc,220,560,nullptr);LineTo(dc,area.right-40,560);SelectObject(dc,previous);DeleteObject(pen);
  }
  auto results=status.GetNamedArray(L"results",JsonArray{});std::wstring text;
  for(auto value:results){auto result=value.GetObject();text+=NativeLinkName(JsonString(result,L"link"));for(auto key:{L"latencyMs",L"downloadMbps",L"uploadMbps"}){wchar_t number[64]{};if(result.HasKey(key)){swprintf_s(number,L"   %.1f %s",result.GetNamedNumber(key),wcscmp(key,L"latencyMs")==0?L"ms":L"Mbps");text+=number;}else text+=L"   —";}text+=L"\n";auto message=JsonString(result,L"message");if(message.empty())message=JsonString(result,L"error");if(!message.empty())text+=message+L"\n";}
  RECT table{220,state->active?580:380,area.right-25,area.bottom-20};DrawTextW(dc,text.c_str(),-1,&table,DT_WORDBREAK);
 }
 SelectObject(dc,font);EndPaint(window,&paint);
}

LRESULT CALLBACK NativeSettingsProc(HWND window,UINT message,WPARAM wParam,LPARAM lParam){
 auto holder=reinterpret_cast<std::shared_ptr<NativeSettingsState>*>(GetWindowLongPtrW(window,GWLP_USERDATA));
 if(message==WM_NCCREATE){auto create=reinterpret_cast<CREATESTRUCTW*>(lParam);holder=reinterpret_cast<std::shared_ptr<NativeSettingsState>*>(create->lpCreateParams);SetWindowLongPtrW(window,GWLP_USERDATA,reinterpret_cast<LONG_PTR>(holder));(*holder)->window=window;}
 if(!holder)return DefWindowProcW(window,message,wParam,lParam);auto state=*holder;
 switch(message){
 case WM_CREATE:{
  SettingsControl(window,L"BUTTON",L("通用"),BS_AUTORADIOBUTTON|BS_PUSHLIKE|WS_GROUP,kSettingsGeneral,20,25,155,40);SettingsControl(window,L"BUTTON",L("网络链路"),BS_AUTORADIOBUTTON|BS_PUSHLIKE,kSettingsLinks,20,75,155,40);
  state->general.push_back(SettingsControl(window,L"BUTTON",L("开机自启"),BS_AUTOCHECKBOX,kSettingsAuto,215,35,400,35));SendMessageW(state->general.back(),BM_SETCHECK,g_autoStart?BST_CHECKED:BST_UNCHECKED,0);
  auto brand=SettingsControl(window,L"STATIC",L"FnCPN",0,0,215,95,350,36);SendMessageW(brand,WM_SETFONT,reinterpret_cast<WPARAM>(g_titleFont),TRUE);state->general.push_back(brand);
  state->buildLabel=SettingsControl(window,L"STATIC",FNCPN_PRODUCT_VERSION,0,0,215,140,365,60);state->general.push_back(state->buildLabel);
  state->general.push_back(SettingsControl(window,L"BUTTON",L("复制"),BS_PUSHBUTTON,8112,600,155,110,32));
  for(int i=0;i<3;i++)state->general.push_back(SettingsControl(window,L"BUTTON",i==0?L"GitHub":i==1?L("官方网站"):L("下载服务端与其他客户端"),BS_PUSHBUTTON,8120+i,215,220+i*50,450,36));
  state->links.push_back(SettingsControl(window,L"STATIC",L("开关与优先级"),0,0,215,20,400,25));SendMessageW(state->links.back(),WM_SETFONT,reinterpret_cast<WPARAM>(g_deviceFont),TRUE);
  auto help=SettingsControl(window,L"BUTTON",L"?",BS_PUSHBUTTON,8111,650,15,28,28);state->links.push_back(help);
  auto tooltip=CreateWindowExW(WS_EX_TOPMOST,TOOLTIPS_CLASSW,nullptr,WS_POPUP|TTS_ALWAYSTIP,CW_USEDEFAULT,CW_USEDEFAULT,CW_USEDEFAULT,CW_USEDEFAULT,window,nullptr,GetModuleHandleW(nullptr),nullptr);
  TOOLINFOW tool{sizeof(tool)};tool.uFlags=TTF_IDISHWND|TTF_SUBCLASS;tool.hwnd=window;tool.uId=reinterpret_cast<UINT_PTR>(help);tool.lpszText=const_cast<wchar_t*>(L("打开或关闭开关可启用或禁用对应链路；拖动远程链路可调整优先级。连接时按从上到下的顺序尝试已启用的链路。局域网始终优先，且不可关闭或调整顺序。"));SendMessageW(tooltip,TTM_ADDTOOLW,0,reinterpret_cast<LPARAM>(&tool));SendMessageW(tooltip,TTM_SETMAXTIPWIDTH,0,330);

  state->list=SettingsControl(window,WC_LISTVIEWW,L"",LVS_REPORT|LVS_SINGLESEL|LVS_NOCOLUMNHEADER,kSettingsOrder,215,50,480,120);state->links.push_back(state->list);for(int i=0;i<3;i++){state->linkSwitches[i]=SettingsControl(window,L"BUTTON",i==0?L("局域网 · 始终优先"):i==1?L("IPv6 直连"):L("FN Connect 中继"),BS_OWNERDRAW|WS_TABSTOP,8140+i,640,50+i*30,48,30);state->links.push_back(state->linkSwitches[i]);}ListView_SetExtendedListViewStyle(state->list,LVS_EX_FULLROWSELECT);LVCOLUMNW column{};column.mask=LVCF_WIDTH;column.cx=410;ListView_InsertColumn(state->list,0,&column);
  state->links.push_back(SettingsControl(window,L"BUTTON",L("恢复默认设置"),BS_PUSHBUTTON,kSettingsReset,215,180,190,32));
  state->links.push_back(SettingsControl(window,L"STATIC",L("网络链路测试"),0,0,215,235,300,25));SendMessageW(state->links.back(),WM_SETFONT,reinterpret_cast<WPARAM>(g_deviceFont),TRUE);state->links.push_back(SettingsControl(window,L"STATIC",L("测试链路"),0,0,215,275,100,25));
  state->selector=SettingsControl(window,L"BUTTON",L"IPv6 / FN Connect  ▾",BS_PUSHBUTTON,kSettingsChoose,310,265,240,36);state->links.push_back(state->selector);
  state->start=SettingsControl(window,L"BUTTON",L("开始测试"),BS_PUSHBUTTON,kSettingsStart,560,265,120,36);state->links.push_back(state->start);
  state->cancelButton=SettingsControl(window,L"BUTTON",L("取消"),BS_PUSHBUTTON,kSettingsCancel,560,265,120,36);state->links.push_back(state->cancelButton);EnableWindow(state->cancelButton,FALSE);ShowWindow(state->cancelButton,SW_HIDE);
  state->notice=SettingsControl(window,L"STATIC",L"",SS_LEFT,0,215,315,330,50);state->links.push_back(state->notice);
  state->choices.push_back(SettingsControl(window,L"BUTTON",L("IPv6 直连"),BS_AUTOCHECKBOX,kSettingsIPv6,310,310,230,30));state->choices.push_back(SettingsControl(window,L"BUTTON",L("FN Connect 中继"),BS_AUTOCHECKBOX,kSettingsRelay,310,345,230,30));state->choices.push_back(SettingsControl(window,L"BUTTON",L("完成"),BS_PUSHBUTTON,kSettingsDone,310,380,100,30));for(int i=0;i<2;i++)SendMessageW(state->choices[i],BM_SETCHECK,BST_CHECKED,0);
  SettingsPage(state,false);SetTimer(window,1,33,nullptr);
  StartWorker([state]{winrt::init_apartment(winrt::apartment_type::multi_threaded);try{auto settings=CallIpc(L"link-settings").GetNamedObject(L"result");auto build=CallIpc(L"build-info").GetNamedObject(L"result");std::lock_guard lock(state->mutex);state->pendingPreferences=settings.Stringify().c_str();state->commit=JsonString(build,L"commit");state->buildInfo=std::wstring(L("版本"))+L" "+JsonString(build,L"version")+L"\nCommit "+state->commit;}catch(const winrt::hresult_error&e){std::lock_guard lock(state->mutex);state->failure=e.message().c_str();}SettingsNotify(state);winrt::uninit_apartment();});return 0;
 }
 case WM_COMMAND:{int command=LOWORD(wParam);
  if(command==kSettingsGeneral||command==kSettingsLinks){SettingsPage(state,command==kSettingsLinks);return 0;}
  if(command==8112){std::lock_guard lock(state->mutex);CopyText(window,state->commit);SetWindowTextW(GetDlgItem(window,8112),L("已复制 Commit"));return 0;}
  if(command==kSettingsAuto){SetAutoStart(SendMessageW(reinterpret_cast<HWND>(lParam),BM_GETCHECK,0,0)==BST_CHECKED);return 0;}
  if(command>=8120&&command<=8122){auto opened=ShellExecuteW(window,L"open",command==8120?L"https://github.com/rectcircle/fn-connect-private-network":command==8121?L"https://fncpn.rectcircle.cn/":L"https://github.com/rectcircle/fn-connect-private-network/releases",nullptr,nullptr,SW_SHOWNORMAL);if(reinterpret_cast<INT_PTR>(opened)<=32)MessageBoxW(window,L("无法打开链接，请检查浏览器设置。"),L("设置"),MB_OK|MB_ICONERROR);return 0;}
  if(command==kSettingsChoose||command==kSettingsDone){state->choosing=command!=kSettingsDone&&!state->choosing;for(auto control:state->choices)ShowWindow(control,state->choosing?SW_SHOW:SW_HIDE);return 0;}
  if(command>=8141&&command<=8142){auto links=state->preferences.GetNamedArray(L"links",JsonArray{});if(links.Size()==2){auto item=links.GetObjectAt(command-8141);item.Insert(L"enabled",JsonValue::CreateBooleanValue(!JsonBool(item,L"enabled")));SettingsRows(state);SettingsSave(state);}return 0;}
  if(command==kSettingsIPv6||command==kSettingsRelay){bool ipv6=SendMessageW(state->choices[0],BM_GETCHECK,0,0)==BST_CHECKED,relay=SendMessageW(state->choices[1],BM_GETCHECK,0,0)==BST_CHECKED;SetWindowTextW(state->selector,ipv6&&relay?L("IPv6 与 FN Connect"):ipv6?L("IPv6 直连"):relay?L("FN Connect 中继"):L("选择测试链路"));return 0;}
  if(command==kSettingsCancel){SetEvent(state->cancellation);return 0;}
  if(command==kSettingsReset){JsonArray links;for(auto key:{L"ipv6",L"fn-connect"}){JsonObject link;link.Insert(L"link",JsonValue::CreateStringValue(key));link.Insert(L"enabled",JsonValue::CreateBooleanValue(true));links.Append(link.as<IJsonValue>());}state->preferences.Insert(L"links",links.as<IJsonValue>());SettingsRows(state);SettingsSave(state);return 0;}
  if(command==kSettingsStart&&!state->active){bool ipv6=SendMessageW(state->choices[0],BM_GETCHECK,0,0)==BST_CHECKED,relay=SendMessageW(state->choices[1],BM_GETCHECK,0,0)==BST_CHECKED;if(!ipv6&&!relay)return 0;
   if(MessageBoxW(window,L("测试期间会重新建立连接，当前网络访问可能短暂中断。\n\n测试结果仅供参考，受当前设备与 NAS 的网络状况、可用带宽及 FN Connect 中转服务器带宽限制。"),L("开始网络链路测试？"),MB_OKCANCEL|MB_ICONINFORMATION)!=IDOK)return 0;
   {std::lock_guard lock(state->mutex);state->failure.clear();state->status=JsonObject{};state->samples.clear();}state->active=true;ResetEvent(state->cancellation);EnableWindow(state->start,FALSE);EnableWindow(state->cancelButton,TRUE);state->choosing=false;for(auto control:state->choices)ShowWindow(control,SW_HIDE);std::wstring link=ipv6&&relay?L"all":ipv6?L"ipv6":L"fn-connect";
   StartWorker([state,link]{winrt::init_apartment(winrt::apartment_type::multi_threaded);try{JsonObject input;input.Insert(L"link",JsonValue::CreateStringValue(link));CallIpc(L"speedtest",&input,[state](const JsonObject& value){{std::lock_guard lock(state->mutex);state->status=value;}SettingsNotify(state);return !JsonBool(value,L"active");},state->cancellation);}catch(const winrt::hresult_error&e){std::lock_guard lock(state->mutex);if(WaitForSingleObject(state->cancellation,0)!=WAIT_OBJECT_0)state->failure=e.message().c_str();}{std::lock_guard lock(state->mutex);state->active=false;}SettingsNotify(state);winrt::uninit_apartment();});return 0;
  }break;
 }
 case WM_DRAWITEM:{auto draw=reinterpret_cast<DRAWITEMSTRUCT*>(lParam);if(draw->CtlID>=8140&&draw->CtlID<=8142){int index=draw->CtlID-8140;auto links=state->preferences.GetNamedArray(L"links",JsonArray{});bool on=index==0||(links.Size()==2&&JsonBool(links.GetObjectAt(index-1),L"enabled"));FillRect(draw->hDC,&draw->rcItem,GetSysColorBrush(COLOR_WINDOW));int y=(draw->rcItem.bottom-20)/2;auto track=CreateSolidBrush(index==0?RGB(160,190,225):on?RGB(0,120,215):GetSysColor(COLOR_GRAYTEXT));auto oldBrush=SelectObject(draw->hDC,track);auto oldPen=SelectObject(draw->hDC,GetStockObject(NULL_PEN));RoundRect(draw->hDC,4,y,44,y+20,20,20);SelectObject(draw->hDC,GetStockObject(WHITE_BRUSH));int x=on?26:6;Ellipse(draw->hDC,x,y+2,x+16,y+18);SelectObject(draw->hDC,oldBrush);SelectObject(draw->hDC,oldPen);DeleteObject(track);if(draw->itemState&ODS_FOCUS)DrawFocusRect(draw->hDC,&draw->rcItem);return TRUE;}break;}
 case WM_NOTIFY:{auto header=reinterpret_cast<NMHDR*>(lParam);if(header->idFrom==kSettingsOrder){if(header->code==NM_CUSTOMDRAW){auto draw=reinterpret_cast<NMLVCUSTOMDRAW*>(lParam);if(draw->nmcd.dwDrawStage==CDDS_PREPAINT)return CDRF_NOTIFYITEMDRAW;if(draw->nmcd.dwDrawStage==CDDS_ITEMPREPAINT&&draw->nmcd.dwItemSpec==0){draw->clrText=GetSysColor(COLOR_GRAYTEXT);return CDRF_NEWFONT;}}if(state->renderingRows)return 0;  if(header->code==LVN_BEGINDRAG){auto drag=reinterpret_cast<NMLISTVIEW*>(lParam);if(drag->iItem>0){state->dragging=drag->iItem;SetCapture(window);}}}break;}
 case WM_LBUTTONUP:if(state->dragging>0){POINT cursor{};GetCursorPos(&cursor);ScreenToClient(state->list,&cursor);LVHITTESTINFO hit{};hit.pt=cursor;int row=ListView_HitTest(state->list,&hit);ReleaseCapture();if(row>0&&row!=state->dragging){auto links=state->preferences.GetNamedArray(L"links");auto first=links.GetAt(0);links.SetAt(0,links.GetAt(1));links.SetAt(1,first);SettingsRows(state);SettingsSave(state);}state->dragging=-1;}return 0;
 case kSettingsUpdate:if(wParam==reinterpret_cast<WPARAM>(state.get())){std::lock_guard lock(state->mutex);if(!state->pendingPreferences.empty()){state->preferences=JsonObject::Parse(state->pendingPreferences);state->pendingPreferences.clear();SettingsRows(state);}
 if(!state->buildInfo.empty())SetWindowTextW(state->buildLabel,state->buildInfo.c_str());auto stage=JsonString(state->status,L"stage");auto family=stage.starts_with(L"upload")?std::wstring(L"upload"):stage;auto link=JsonString(state->status,L"link");
 if(link!=state->sampleLink||((family==L"upload"||family==L"download")&&family!=state->sampleStage))state->samples.clear();state->sampleLink=link;
 if(family==L"upload"||family==L"download"){
  state->sampleStage=family;state->sampleWindow=std::max(1.0,state->status.GetNamedNumber(L"targetSampleMs",8000));auto elapsed=state->status.GetNamedNumber(L"sampleElapsedMs",0);
  if(elapsed>0&&(state->samples.empty()||elapsed>state->samples.back().elapsed)){state->samples.push_back({elapsed,state->status.GetNamedNumber(L"mbps",0)});auto cutoff=std::max(0.0,elapsed-state->sampleWindow);while(state->samples.size()>1&&state->samples[1].elapsed<=cutoff)state->samples.erase(state->samples.begin());}
 }
 std::wstring phase=stage;for(auto pair:std::initializer_list<std::pair<const wchar_t*,const wchar_t*>>{{L"connecting",L("正在建立真实链路")},{L"latency",L("正在测试延迟")},{L"download",L("正在测试下载")},{L"upload",L("正在测试上传")},{L"restoring",L("正在恢复连接")},{L"completed",L("测试完成")},{L"canceled",L("已取消")}}){if(stage==pair.first)phase=pair.second;}
 SetWindowTextW(state->notice,state->failure.empty()?phase.c_str():state->failure.c_str());EnableWindow(state->start,!state->active);EnableWindow(state->cancelButton,state->active);ShowWindow(state->start,state->linkPage&&!state->active?SW_SHOW:SW_HIDE);ShowWindow(state->cancelButton,state->linkPage&&state->active?SW_SHOW:SW_HIDE);InvalidateRect(window,nullptr,TRUE);}return 0;
 case WM_TIMER:if(state->active)InvalidateRect(window,nullptr,FALSE);return 0;
 case WM_PAINT:{std::lock_guard lock(state->mutex);SettingsPaint(window,state);return 0;}
 case WM_CLOSE:DestroyWindow(window);return 0;
 case WM_NCDESTROY:{SetEvent(state->cancellation);{std::lock_guard lock(state->mutex);state->window=nullptr;}g_nativeSettings=nullptr;SetWindowLongPtrW(window,GWLP_USERDATA,0);delete holder;return DefWindowProcW(window,message,wParam,lParam);}
 }
 return DefWindowProcW(window,message,wParam,lParam);
}
void OpenNativeSettings(){if(g_nativeSettings){ShowWindow(g_nativeSettings,SW_SHOW);SetForegroundWindow(g_nativeSettings);return;}WNDCLASSW klass{};klass.lpfnWndProc=NativeSettingsProc;klass.hInstance=GetModuleHandleW(nullptr);klass.lpszClassName=L"FnCPN.NativeSettings";klass.hCursor=LoadCursorW(nullptr,IDC_ARROW);klass.hbrBackground=reinterpret_cast<HBRUSH>(COLOR_WINDOW+1);RegisterClassW(&klass);auto state=new std::shared_ptr<NativeSettingsState>(std::make_shared<NativeSettingsState>());g_nativeSettings=CreateWindowExW(WS_EX_APPWINDOW,klass.lpszClassName,L("设置"),WS_OVERLAPPED|WS_CAPTION|WS_SYSMENU|WS_MINIMIZEBOX,CW_USEDEFAULT,CW_USEDEFAULT,780,780,g_window,nullptr,klass.hInstance,state);if(!g_nativeSettings){delete state;return;}ShowWindow(g_nativeSettings,SW_SHOW);}
