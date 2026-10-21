// Exercise the real GDBus filter on a private session bus, without GTK/display.
#include "activation_linux.h"

#define ITEM "/org/ayatana/NotificationItem/fncpn"
static GDBusConnection *client;
static gchar *destination;

static gpointer run_loop(gpointer data){g_main_loop_run(data);return NULL;}
static GVariant *call(const gchar *path,const gchar *interface,const gchar *method,GVariant *body,GError **error){
 return g_dbus_connection_call_sync(client,destination,path,interface,method,body,NULL,
                                   G_DBUS_CALL_FLAGS_NONE,3000,NULL,error);
}
static void provide(const gchar *path,const gchar *token,gboolean succeeds){
 GError *error=NULL;
 GVariant *reply=call(path,"org.kde.StatusNotifierItem","ProvideXdgActivationToken",g_variant_new("(s)",token),&error);
 if(succeeds){g_assert_no_error(error);g_assert_nonnull(reply);}
 else{g_assert_nonnull(error);g_assert_null(reply);g_clear_error(&error);}
 if(reply)g_variant_unref(reply);
}
static void click(guint32 timestamp){
 // No DBusMenu server is registered: its default error proves the filter
 // observes Event without swallowing it. The timestamp must still be retained.
 GError *error=NULL;
 GVariant *reply=call(ITEM "/Menu","com.canonical.dbusmenu","Event",
                    g_variant_new("(isvu)",1,"clicked",g_variant_new_int32(0),timestamp),&error);
 g_assert_null(reply);g_assert_nonnull(error);g_clear_error(&error);
}
int main(int argc,char **argv){
 g_test_init(&argc,&argv,NULL);
 GTestDBus *bus=g_test_dbus_new(G_TEST_DBUS_NONE);g_test_dbus_up(bus);
 GError *error=NULL;g_assert_true(fn_activation_init(&error));g_assert_no_error(error);
 GDBusConnection *server=g_bus_get_sync(G_BUS_TYPE_SESSION,NULL,&error);g_assert_no_error(error);
 destination=g_strdup(g_dbus_connection_get_unique_name(server));g_object_unref(server);
 GMainLoop *loop=g_main_loop_new(NULL,FALSE);GThread *thread=g_thread_new("activation-test",run_loop,loop);
 client=g_dbus_connection_new_for_address_sync(g_test_dbus_get_bus_address(bus),
  G_DBUS_CONNECTION_FLAGS_AUTHENTICATION_CLIENT|G_DBUS_CONNECTION_FLAGS_MESSAGE_BUS_CONNECTION,NULL,NULL,&error);
 g_assert_no_error(error);
 provide(ITEM,"test-compositor-token",TRUE);click(12345);
 guint32 timestamp=0;gchar *token=fn_activation_take(&timestamp);
 g_assert_cmpstr(token,==,"test-compositor-token");g_assert_cmpuint(timestamp,==,12345);g_free(token);
 token=fn_activation_take(&timestamp);g_assert_null(token);g_assert_cmpuint(timestamp,==,0);
 provide("/other/item","wrong-window-token",FALSE);
 provide(ITEM,"",FALSE);
 token=fn_activation_take(&timestamp);g_assert_null(token);
 click(54321);token=fn_activation_take(&timestamp);g_assert_null(token);g_assert_cmpuint(timestamp,==,54321);
 fn_activation_dispose();g_dbus_connection_close_sync(client,NULL,NULL);g_object_unref(client);
 g_free(destination);g_main_loop_quit(loop);g_thread_join(thread);g_main_loop_unref(loop);
 g_test_dbus_down(bus);g_object_unref(bus);
 g_print("Activation token delivery, timestamp forwarding and one-shot consumption: passed\n");
 return 0;
}
