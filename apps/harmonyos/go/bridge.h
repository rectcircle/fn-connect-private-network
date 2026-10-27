#include <stdlib.h>
typedef char *(*harmony_system_fn)(const char *);
void harmony_set_system(harmony_system_fn callback);
char *harmony_system_call(const char *request);
