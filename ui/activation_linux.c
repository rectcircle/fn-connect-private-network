// Wayland activation support for the legacy GTK AppIndicator implementation.
#include "activation_linux.h"
#include <string.h>

#define ITEM_PATH "/org/ayatana/NotificationItem/fncpn"
#define MENU_PATH ITEM_PATH "/Menu"
#define TOKEN_MAX_BYTES 4096
#define ACTIVATION_MAX_AGE (5 * G_USEC_PER_SEC)

static GDBusConnection *activation_bus;
static guint activation_filter;
static GMutex activation_lock;
static gchar *pending_token;
static guint32 pending_timestamp;
static gint64 token_received_at, event_received_at;
static gboolean activation_enabled;

// Filters run on GDBus' worker thread. No GTK calls are allowed here.
static GDBusMessage *activation_message(GDBusConnection *bus, GDBusMessage *message,
                                       gboolean incoming, gpointer unused) {
 (void)unused;
 if(!incoming || g_dbus_message_get_message_type(message)!=G_DBUS_MESSAGE_TYPE_METHOD_CALL)
  return message;
 const gchar *path=g_dbus_message_get_path(message);
 const gchar *interface=g_dbus_message_get_interface(message);
 const gchar *method=g_dbus_message_get_member(message);
 GVariant *body=g_dbus_message_get_body(message);
 if(g_strcmp0(path,ITEM_PATH)==0 && g_strcmp0(interface,"org.kde.StatusNotifierItem")==0 &&
    g_strcmp0(method,"ProvideXdgActivationToken")==0){
  // GNOME supplies this method before a tray click. The old library would
  // otherwise reply UnknownMethod and GNOME would stop supplying tokens.
  if(!body || !g_variant_is_of_type(body,G_VARIANT_TYPE("(s)")))return message;
  const gchar *token;g_variant_get(body,"(&s)",&token);
  if(!*token || strlen(token)>TOKEN_MAX_BYTES)return message;
  g_mutex_lock(&activation_lock);
  if(!activation_enabled){g_mutex_unlock(&activation_lock);return message;}
  g_free(pending_token);pending_token=g_strdup(token);
  token_received_at=g_get_monotonic_time();
  g_mutex_unlock(&activation_lock);
  GDBusMessage *reply=g_dbus_message_new_method_reply(message);
  g_dbus_connection_send_message(bus,reply,G_DBUS_SEND_MESSAGE_FLAGS_NONE,NULL,NULL);
  g_object_unref(reply);
  // A dropped filter message is owned by the filter.
  g_object_unref(message);
  return NULL;
 }
 if(g_strcmp0(path,MENU_PATH)==0 && g_strcmp0(interface,"com.canonical.dbusmenu")==0 &&
    g_strcmp0(method,"Event")==0 && body && g_variant_is_of_type(body,G_VARIANT_TYPE("(isvu)"))){
  gint32 id;const gchar *event;GVariant *value;guint32 timestamp;
  g_variant_get(body,"(i&svu)",&id,&event,&value,&timestamp);
  if(g_strcmp0(event,"clicked")==0 && timestamp){
   g_mutex_lock(&activation_lock);
   if(activation_enabled){pending_timestamp=timestamp;event_received_at=g_get_monotonic_time();}
   g_mutex_unlock(&activation_lock);
  }
  g_variant_unref(value);
 }
 return message; // Let AppIndicator/DBusMenu handle the actual menu action.
}

gboolean fn_activation_init(GError **error){
 activation_bus=g_bus_get_sync(G_BUS_TYPE_SESSION,NULL,error);
 if(!activation_bus)return FALSE;
 g_mutex_lock(&activation_lock);activation_enabled=TRUE;g_mutex_unlock(&activation_lock);
 // g_bus_get returns the same shared connection used by AppIndicator.
 activation_filter=g_dbus_connection_add_filter(activation_bus,activation_message,NULL,NULL);
 return TRUE;
}

gchar *fn_activation_take(guint32 *timestamp){
 gint64 now=g_get_monotonic_time();
 g_mutex_lock(&activation_lock);
 gchar *token=NULL;
 if(pending_token && now-token_received_at<=ACTIVATION_MAX_AGE)
  token=g_steal_pointer(&pending_token);
 g_clear_pointer(&pending_token,g_free);
 *timestamp=(now-event_received_at<=ACTIVATION_MAX_AGE)?pending_timestamp:0;
 pending_timestamp=0;event_received_at=0;token_received_at=0;
 g_mutex_unlock(&activation_lock);
 return token;
}

void fn_activation_dispose(void){
 g_mutex_lock(&activation_lock);
 activation_enabled=FALSE;g_clear_pointer(&pending_token,g_free);pending_timestamp=0;
 g_mutex_unlock(&activation_lock);
 if(activation_bus){
  g_dbus_connection_remove_filter(activation_bus,activation_filter);
  g_clear_object(&activation_bus);
 }
}
