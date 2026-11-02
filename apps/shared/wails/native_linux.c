// Linux GTK3 integration. Every GTK operation is scheduled on Wails' GTK loop.
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>
#ifdef FNCPN_APPINDICATOR_LEGACY
#include <libappindicator/app-indicator.h>
#else
#include <libayatana-appindicator/app-indicator.h>
#endif
#include <string.h>
#include <stdlib.h>
extern char *fnGoSettings(char*);
#include "activation_linux.h"
#ifdef GDK_WINDOWING_WAYLAND
#include <gdk/gdkwayland.h>
#endif
extern void fnGoAction(char*);
static AppIndicator *indicator;
static GtkWidget *connection, *relogin, *autostart, *admin_window, *admin_view;
static int refreshing;
static const char *label(const char *zh,const char *en){return g_str_has_prefix(g_get_language_names()[0],"zh")?zh:en;}
static gboolean present_main(gpointer event_time){
 GList *windows=gtk_window_list_toplevels();
 for(GList *entry=windows;entry;entry=entry->next){
  GtkWindow *window=GTK_WINDOW(entry->data);
  if(g_strcmp0(gtk_window_get_title(window),"FnCPN"))continue;
  guint32 menu_time=0;
  gchar *token=fn_activation_take(&menu_time);
  guint32 timestamp=menu_time?menu_time:GPOINTER_TO_UINT(event_time);
  gtk_window_deiconify(window);gtk_widget_show(GTK_WIDGET(window));
#ifdef GDK_WINDOWING_WAYLAND
  GdkDisplay *display=gtk_widget_get_display(GTK_WIDGET(window));
  if(GDK_IS_WAYLAND_DISPLAY(display)){
   GdkWindow *surface=gtk_widget_get_window(GTK_WIDGET(window));
   if(surface)gdk_wayland_window_set_application_id(surface,"fncpn");
   // GDK consumes this token when present_with_time requests Wayland focus.
   if(token)gdk_wayland_display_set_startup_notification_id(display,token);
  }else
#endif
  if(token)gtk_window_set_startup_id(window,token);
  gtk_window_present_with_time(window,timestamp);
  g_free(token);
  break;
 }
 g_list_free(windows);return G_SOURCE_REMOVE;
}
void fn_present_main(void){g_idle_add(present_main,GUINT_TO_POINTER(GDK_CURRENT_TIME));}
static void action(GtkMenuItem *item,gpointer name){
 if(refreshing)return;
 // DBusMenu activation has no GdkEvent; present_main also uses the original
 // timestamp and token retained by the D-Bus filter.
 if(!strcmp((const char*)name,"show")){present_main(GUINT_TO_POINTER(gtk_get_current_event_time()));return;}
 fnGoAction((char*)name);
}
static GtkWidget *menu_item(GtkWidget *menu,const char *label,const char *name){GtkWidget *item=gtk_menu_item_new_with_label(label);g_signal_connect(item,"activate",G_CALLBACK(action),(gpointer)name);gtk_menu_shell_append(GTK_MENU_SHELL(menu),item);return item;}
// macOS template PNGs stay black on GNOME; symbolic SVGs follow the panel theme.
static gboolean setup(gpointer icon_path){
 GError *activation_error=NULL;
 if(!fn_activation_init(&activation_error)){
  g_warning("FnCPN activation bus: %s",activation_error->message);g_clear_error(&activation_error);
 }
 indicator=app_indicator_new("fncpn","fncpn-offline-symbolic",APP_INDICATOR_CATEGORY_APPLICATION_STATUS);
 app_indicator_set_icon_theme_path(indicator,(const char*)icon_path);g_free(icon_path);
 GtkWidget *menu=gtk_menu_new();menu_item(menu,label("打开 FnCPN","Open FnCPN"),"show");
 connection=menu_item(menu,label("连接","Connect"),"primary");relogin=menu_item(menu,label("重新登录","Sign in again"),"login");
 autostart=gtk_check_menu_item_new_with_label(label("登录时启动","Launch at login"));g_signal_connect(autostart,"activate",G_CALLBACK(action),"autostart");gtk_menu_shell_append(GTK_MENU_SHELL(menu),autostart);
 menu_item(menu,label("官网 · 捐赠","Website / Donate"),"homepage");menu_item(menu,label("设置","Settings"),"settings");menu_item(menu,label("退出","Quit"),"quit");gtk_widget_show_all(menu);
 app_indicator_set_menu(indicator,GTK_MENU(menu));app_indicator_set_status(indicator,APP_INDICATOR_STATUS_ACTIVE);return G_SOURCE_REMOVE;
}
void fn_init(char *icon_path){g_idle_add(setup,g_strdup(icon_path));}
static gboolean dispose(gpointer unused){fn_activation_dispose();if(admin_window)gtk_widget_destroy(admin_window);admin_window=admin_view=NULL;if(indicator){app_indicator_set_status(indicator,APP_INDICATOR_STATUS_PASSIVE);g_object_unref(indicator);indicator=NULL;}return G_SOURCE_REMOVE;}
void fn_dispose(void){g_idle_add(dispose,NULL);}
typedef struct {char *state,*title,*action;int needs_login,busy,auto_start;} TrayState;
static gboolean update(gpointer data){TrayState *value=data;if(indicator){
 const char *icon="fncpn-offline-symbolic";
 if(!strcmp(value->state,"LOCAL")||!strcmp(value->state,"DIRECT")||!strcmp(value->state,"RELAY"))icon="fncpn-connected-symbolic";
 if(value->busy||!strcmp(value->state,"PROBING")||!strcmp(value->state,"RECONNECTING"))icon="fncpn-working-symbolic";
 if(!strcmp(value->state,"ERROR")||!strcmp(value->state,"AUTH_REQUIRED")||!strcmp(value->state,"DAEMON_UNAVAILABLE")||!strcmp(value->state,"SERVER_UNAVAILABLE"))icon="fncpn-attention-symbolic";
 app_indicator_set_icon_full(indicator,icon,value->title);app_indicator_set_title(indicator,value->title);
 gtk_menu_item_set_label(GTK_MENU_ITEM(connection),value->action);gtk_widget_set_sensitive(connection,!value->busy);
 gtk_widget_set_visible(relogin,!value->needs_login);gtk_widget_set_sensitive(relogin,!value->busy);
 refreshing=1;gtk_check_menu_item_set_active(GTK_CHECK_MENU_ITEM(autostart),value->auto_start);refreshing=0;
 }g_free(value->state);g_free(value->title);g_free(value->action);g_free(value);return G_SOURCE_REMOVE;}
void fn_tray(char *state,char *title,char *action_label,int needs_login,int busy,int auto_start){TrayState *value=g_new0(TrayState,1);value->state=g_strdup(state);value->title=g_strdup(title);value->action=g_strdup(action_label);value->needs_login=needs_login;value->busy=busy;value->auto_start=auto_start;g_idle_add(update,value);}
static char *settings_origin;
static void settings_message(WebKitUserContentManager *manager,WebKitJavascriptResult *result,gpointer unused){
 const char *uri=admin_view?webkit_web_view_get_uri(WEBKIT_WEB_VIEW(admin_view)):NULL;
 if(!uri || !settings_origin || !g_str_has_prefix(uri,settings_origin))return;
 JSCValue *value=webkit_javascript_result_get_js_value(result);
 if(!jsc_value_is_string(value))return;
 char *action=jsc_value_to_string(value);char *reply=fnGoSettings(action);g_free(action);
 if(reply){char *script=g_strconcat("window.fncpnSettingsState(",reply,")",NULL);webkit_web_view_run_javascript(WEBKIT_WEB_VIEW(admin_view),script,NULL,NULL,NULL);g_free(script);free(reply);}
}
static gboolean settings_navigation(WebKitWebView *view,WebKitPolicyDecision *decision,WebKitPolicyDecisionType type,gpointer unused){
 if(type!=WEBKIT_POLICY_DECISION_TYPE_NAVIGATION_ACTION && type!=WEBKIT_POLICY_DECISION_TYPE_NEW_WINDOW_ACTION)return FALSE;
 WebKitNavigationAction *action=webkit_navigation_policy_decision_get_navigation_action(WEBKIT_NAVIGATION_POLICY_DECISION(decision));
 const char *uri=webkit_uri_request_get_uri(webkit_navigation_action_get_request(action));
 if(settings_origin && g_str_has_prefix(uri,settings_origin))return FALSE;
 webkit_policy_decision_ignore(decision);
 if(webkit_navigation_action_is_user_gesture(action) && (g_str_has_prefix(uri,"https://github.com/rectcircle/fn-connect-private-network") || g_str_has_prefix(uri,"https://fncpn.rectcircle.cn/")))gtk_show_uri_on_window(GTK_WINDOW(admin_window),uri,GDK_CURRENT_TIME,NULL);
 return TRUE;
}
static void admin_destroyed(GtkWidget *widget,gpointer unused){admin_window=admin_view=NULL;g_clear_pointer(&settings_origin,g_free);}
static gboolean open_admin(gpointer address){
 const gboolean settings=strstr((char*)address,"/app/fncpn/settings?")!=NULL;
 if(admin_window)gtk_widget_destroy(admin_window);
 admin_window=gtk_window_new(GTK_WINDOW_TOPLEVEL);gtk_window_set_title(GTK_WINDOW(admin_window),settings?label("设置","Settings"):label("FnCPN 管理后台","FnCPN Administration"));gtk_window_set_default_size(GTK_WINDOW(admin_window),900,720);g_signal_connect(admin_window,"destroy",G_CALLBACK(admin_destroyed),NULL);
 WebKitUserContentManager *manager=webkit_user_content_manager_new();
 if(settings){char *query=strchr((char*)address,'?');settings_origin=g_strndup(address,query-(char*)address);g_signal_connect(manager,"script-message-received::fncpnSettings",G_CALLBACK(settings_message),NULL);webkit_user_content_manager_register_script_message_handler(manager,"fncpnSettings");}
 admin_view=webkit_web_view_new_with_user_content_manager(manager);if(settings)g_signal_connect(admin_view,"decide-policy",G_CALLBACK(settings_navigation),NULL);g_object_unref(manager);gtk_container_add(GTK_CONTAINER(admin_window),admin_view);
 webkit_web_view_load_uri(WEBKIT_WEB_VIEW(admin_view),(char*)address);gtk_widget_show_all(admin_window);gtk_window_present(GTK_WINDOW(admin_window));g_free(address);return G_SOURCE_REMOVE;
}
void fn_admin(char *address){g_idle_add(open_admin,g_strdup(address));}
