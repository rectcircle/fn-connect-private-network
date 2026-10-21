#ifndef FNCPN_LINUX_ACTIVATION_H
#define FNCPN_LINUX_ACTIVATION_H
#include <gio/gio.h>

// Add the standard activation-token method missing from older AppIndicator
// implementations, and retain the DBusMenu event's original user timestamp.
gboolean fn_activation_init(GError **error);
void fn_activation_dispose(void);
gchar *fn_activation_take(guint32 *timestamp);
#endif
