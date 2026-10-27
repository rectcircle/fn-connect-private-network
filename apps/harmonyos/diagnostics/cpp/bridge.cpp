#include <napi/native_api.h>
#include <dlfcn.h>
#include <pthread.h>
#include <string>
#include <vector>
#include <mutex>

static std::once_flag initialized;
static void *library;
static int (*smoke)();
static char *(*run)(char *);
static char *(*vpn)(int,int);
static void (*release)(char *);
static std::string loadError;
struct Job { napi_async_work work; napi_deferred deferred; std::string url, result, error; int action=0, fd=-1; };
static void Initialize() {
 library = dlopen("libharmonygo.so", RTLD_NOW | RTLD_LOCAL);
 if (!library) { loadError = dlerror(); return; }
 smoke = reinterpret_cast<int (*)()>(dlsym(library,"HarmonySmoke"));
 run = reinterpret_cast<char *(*)(char *)>(dlsym(library,"HarmonyRun"));
 vpn = reinterpret_cast<char *(*)(int,int)>(dlsym(library,"HarmonyVPN"));
 release = reinterpret_cast<void (*)(char *)>(dlsym(library,"HarmonyFree"));
 if (!smoke || !run || !release || !vpn) loadError = "Missing Go exports";
 // The Go runtime remains loaded for the entire process lifetime.
}
static void *ThreadProbe(void *value) {
 auto count = static_cast<int *>(value);
 for(int i=0;i<25;i++) if(smoke()==42) ++*count;
 return nullptr;
}
static void Execute(napi_env, void *data) {
 auto job=static_cast<Job *>(data);
 std::call_once(initialized, Initialize);
 if(!loadError.empty()) { job->error=loadError; return; }
 if(job->action) { char *value=vpn(job->action,job->fd); if(value){job->result=value;release(value);}else{job->error="Empty VPN result";} return; }
 pthread_t threads[4]; int counts[4]={}; int created=0;
 for(int i=0;i<4;i++) {
  if(pthread_create(&threads[i],nullptr,ThreadProbe,&counts[i])!=0) { job->error="pthread_create failed"; break; }
  created++;
 }
 int total=0;
 for(int i=0;i<created;i++) { pthread_join(threads[i],nullptr); total+=counts[i]; }
 if(!job->error.empty()) return;
 if(total!=100) { job->error="Foreign thread callback count mismatch"; return; }
 char *value=run(job->url.data());
 if(!value) { job->error="Empty Go result"; return; }
 job->result=value;release(value);
}
static void Complete(napi_env env,napi_status status,void *data) {
 auto job=static_cast<Job *>(data); napi_value value;
 if(status!=napi_ok || !job->error.empty()) {
  std::string error=job->error.empty()?"Async work failed":job->error;
  napi_create_string_utf8(env,error.c_str(),NAPI_AUTO_LENGTH,&value);
  napi_reject_deferred(env,job->deferred,value);
 } else {
  napi_create_string_utf8(env,job->result.c_str(),job->result.size(),&value);
  napi_resolve_deferred(env,job->deferred,value);
 }
 napi_delete_async_work(env,job->work);delete job;
}
static napi_value Run(napi_env env,napi_callback_info info) {
 size_t argc=1; napi_value argv[1],promise,name;
 napi_get_cb_info(env,info,&argc,argv,nullptr,nullptr);
 auto job=new Job{};
 if(argc) {
  size_t size=0;
  if(napi_get_value_string_utf8(env,argv[0],nullptr,0,&size)!=napi_ok || size>2048) {
   delete job;napi_throw_type_error(env,nullptr,"URL must be a string of at most 2048 bytes");return nullptr;
  }
  std::vector<char> buffer(size+1);
  napi_get_value_string_utf8(env,argv[0],buffer.data(),buffer.size(),&size);job->url.assign(buffer.data(),size);
 }
 napi_create_promise(env,&job->deferred,&promise);
 napi_create_string_utf8(env,"HarmonyGoSmoke",NAPI_AUTO_LENGTH,&name);
 if(napi_create_async_work(env,nullptr,name,Execute,Complete,job,&job->work)!=napi_ok) {
  delete job;napi_throw_error(env,nullptr,"Cannot create async work");return nullptr;
 }
 if(napi_queue_async_work(env,job->work)!=napi_ok) {
  napi_delete_async_work(env,job->work);delete job;napi_throw_error(env,nullptr,"Cannot queue async work");return nullptr;
 }
 return promise;
}
static napi_value Vpn(napi_env env,napi_callback_info info) {
 size_t argc=2; napi_value argv[2],promise,name;
 napi_get_cb_info(env,info,&argc,argv,nullptr,nullptr);
 auto job=new Job{};
 if(argc!=2 || napi_get_value_int32(env,argv[0],&job->action)!=napi_ok ||
    napi_get_value_int32(env,argv[1],&job->fd)!=napi_ok || job->action<1 || job->action>6) {
  delete job;napi_throw_type_error(env,nullptr,"VPN diagnostic action and fd required");return nullptr;
 }
 napi_create_promise(env,&job->deferred,&promise);
 napi_create_string_utf8(env,"HarmonyVpnProbe",NAPI_AUTO_LENGTH,&name);
 if(napi_create_async_work(env,nullptr,name,Execute,Complete,job,&job->work)!=napi_ok) {
  delete job;napi_throw_error(env,nullptr,"Cannot create VPN work");return nullptr;
 }
 if(napi_queue_async_work(env,job->work)!=napi_ok) {
  napi_delete_async_work(env,job->work);delete job;napi_throw_error(env,nullptr,"Cannot queue VPN work");return nullptr;
 }
 return promise;
}
static napi_value Init(napi_env env,napi_value exports) {
 napi_property_descriptor methods[]={{"run",nullptr,Run,nullptr,nullptr,nullptr,napi_default,nullptr},{"vpn",nullptr,Vpn,nullptr,nullptr,nullptr,napi_default,nullptr}};
 napi_define_properties(env,exports,2,methods);return exports;
}
static napi_module module={1,0,nullptr,Init,"harmonybridge",nullptr,{0}};
extern "C" __attribute__((constructor)) void Register() { napi_module_register(&module); }
