//go:build ios
#include "bridge.h"
static fncpn_system_fn system_callback;
static fncpn_packet_fn packet_callback;
void fncpn_set_callbacks(fncpn_system_fn s, fncpn_packet_fn p) { system_callback=s; packet_callback=p; }
char *fncpn_system_call(const char *r) { return system_callback ? system_callback(r) : NULL; }
int fncpn_packet_write(const void *p, int n) { return packet_callback ? packet_callback(p,n) : -1; }
