// Linux GTK3 integration. Every GTK operation is scheduled on Wails' GTK loop.
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>
#include <libayatana-appindicator/app-indicator.h>
#include <string.h>
extern void fnGoAction(char*);
static AppIndicator *indicator;
static GtkWidget *connection, *relogin, *autostart, *admin_window, *admin_view;
static int refreshing;
static const char *label(const char *zh,const char *en){return g_str_has_prefix(g_get_language_names()[0],"zh")?zh:en;}
static void action(GtkMenuItem *item,gpointer name){if(!refreshing)fnGoAction((char*)name);}
static GtkWidget *menu_item(GtkWidget *menu,const char *label,const char *name){GtkWidget *item=gtk_menu_item_new_with_label(label);g_signal_connect(item,"activate",G_CALLBACK(action),(gpointer)name);gtk_menu_shell_append(GTK_MENU_SHELL(menu),item);return item;}
static gboolean setup(gpointer icon_path){
 indicator=app_indicator_new("fncpn","Status-offline",APP_INDICATOR_CATEGORY_APPLICATION_STATUS);
 app_indicator_set_icon_theme_path(indicator,(const char*)icon_path);g_free(icon_path);
 GtkWidget *menu=gtk_menu_new();menu_item(menu,label("打开 FnCPN","Open FnCPN"),"show");
 connection=menu_item(menu,label("连接","Connect"),"primary");relogin=menu_item(menu,label("重新登录","Sign in again"),"login");
 autostart=gtk_check_menu_item_new_with_label(label("登录时启动","Launch at login"));g_signal_connect(autostart,"activate",G_CALLBACK(action),"autostart");gtk_menu_shell_append(GTK_MENU_SHELL(menu),autostart);
 menu_item(menu,label("官网 · 捐赠","Website / Donate"),"homepage");menu_item(menu,label("退出","Quit"),"quit");gtk_widget_show_all(menu);
 app_indicator_set_menu(indicator,GTK_MENU(menu));app_indicator_set_status(indicator,APP_INDICATOR_STATUS_ACTIVE);return G_SOURCE_REMOVE;
}
void fn_init(char *icon_path){g_idle_add(setup,g_strdup(icon_path));}
static gboolean dispose(gpointer unused){if(admin_window)gtk_widget_destroy(admin_window);admin_window=admin_view=NULL;if(indicator){app_indicator_set_status(indicator,APP_INDICATOR_STATUS_PASSIVE);g_object_unref(indicator);indicator=NULL;}return G_SOURCE_REMOVE;}
void fn_dispose(void){g_idle_add(dispose,NULL);}
typedef struct {char *state,*title,*action;int needs_login,busy,auto_start;} TrayState;
static gboolean update(gpointer data){TrayState *value=data;if(indicator){
 const char *icon="Status-offline";
 if(!strcmp(value->state,"LOCAL")||!strcmp(value->state,"DIRECT")||!strcmp(value->state,"RELAY"))icon="Status-connected";
 if(value->busy||!strcmp(value->state,"PROBING")||!strcmp(value->state,"RECONNECTING"))icon="Status-working";
 if(!strcmp(value->state,"ERROR")||!strcmp(value->state,"AUTH_REQUIRED")||!strcmp(value->state,"DAEMON_UNAVAILABLE")||!strcmp(value->state,"SERVER_UNAVAILABLE"))icon="Status-attention";
 app_indicator_set_icon_full(indicator,icon,value->title);app_indicator_set_title(indicator,value->title);
 gtk_menu_item_set_label(GTK_MENU_ITEM(connection),value->action);gtk_widget_set_sensitive(connection,!value->busy);
 gtk_widget_set_visible(relogin,!value->needs_login);gtk_widget_set_sensitive(relogin,!value->busy);
 refreshing=1;gtk_check_menu_item_set_active(GTK_CHECK_MENU_ITEM(autostart),value->auto_start);refreshing=0;
 }g_free(value->state);g_free(value->title);g_free(value->action);g_free(value);return G_SOURCE_REMOVE;}
void fn_tray(char *state,char *title,char *action_label,int needs_login,int busy,int auto_start){TrayState *value=g_new0(TrayState,1);value->state=g_strdup(state);value->title=g_strdup(title);value->action=g_strdup(action_label);value->needs_login=needs_login;value->busy=busy;value->auto_start=auto_start;g_idle_add(update,value);}
static gboolean open_admin(gpointer address){
 if(!admin_window){admin_window=gtk_window_new(GTK_WINDOW_TOPLEVEL);gtk_window_set_title(GTK_WINDOW(admin_window),label("FnCPN 管理后台","FnCPN Administration"));gtk_window_set_default_size(GTK_WINDOW(admin_window),1000,720);g_signal_connect(admin_window,"delete-event",G_CALLBACK(gtk_widget_hide_on_delete),NULL);
 // A separate WebKit view has no Wails bridge or injected local bindings.
 admin_view=webkit_web_view_new();gtk_container_add(GTK_CONTAINER(admin_window),admin_view);}
 webkit_web_view_load_uri(WEBKIT_WEB_VIEW(admin_view),(char*)address);gtk_widget_show_all(admin_window);gtk_window_present(GTK_WINDOW(admin_window));g_free(address);return G_SOURCE_REMOVE;
}
void fn_admin(char *address){g_idle_add(open_admin,g_strdup(address));}
