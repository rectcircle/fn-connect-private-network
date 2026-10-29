#include <stdlib.h>
#include <stdint.h>
typedef char *(*fncpn_system_fn)(const char *request);
typedef int (*fncpn_packet_fn)(const void *bytes, int length);
void fncpn_set_callbacks(fncpn_system_fn system, fncpn_packet_fn packet);
char *fncpn_system_call(const char *request);
int fncpn_packet_write(const void *bytes, int length);
