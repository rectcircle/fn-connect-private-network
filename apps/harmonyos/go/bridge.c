//go:build openharmony

#include "bridge.h"
static harmony_system_fn system_callback;
void harmony_set_system(harmony_system_fn callback) { system_callback = callback; }
char *harmony_system_call(const char *request) { return system_callback ? system_callback(request) : NULL; }
