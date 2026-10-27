#include <napi/native_api.h>
#include <dlfcn.h>
#include <hilog/log.h>
#include <condition_variable>
#include <chrono>
#include <cstdlib>
#include <cstring>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <atomic>

struct Pending { std::mutex mutex; std::condition_variable cv; bool ready=false; std::string request,response,id; };
static std::mutex pendingMutex;
static std::map<std::string,std::shared_ptr<Pending>> pending;
static napi_threadsafe_function dispatch=nullptr;
static std::atomic<unsigned long> sequence{0};
static void *library=nullptr;
static char *(*host)(int,char *)=nullptr;
static void (*freeGo)(char *)=nullptr;
static std::once_flag loaded;
static std::string loadError;
static char *SystemCall(const char *request) {
 auto item=std::make_shared<Pending>();item->request=request;item->id=std::to_string(++sequence);
 {
  std::lock_guard<std::mutex> lock(pendingMutex);
  if(!dispatch||pending.size()>=64)return nullptr;
  pending[item->id]=item;
  auto payload=new std::shared_ptr<Pending>(item);
  if(napi_call_threadsafe_function(dispatch,payload,napi_tsfn_nonblocking)!=napi_ok){delete payload;pending.erase(item->id);return nullptr;}
 }
 std::unique_lock<std::mutex> lock(item->mutex);
 bool done=item->cv.wait_for(lock,std::chrono::seconds(30),[&]{return item->ready;});
 std::string response=item->response;lock.unlock();
 {std::lock_guard<std::mutex> guard(pendingMutex);pending.erase(item->id);}
 return done?strdup(response.c_str()):nullptr;
}
static void Load(){
 library=dlopen("libfncpn.so",RTLD_NOW|RTLD_LOCAL);if(!library){loadError=dlerror();return;}
 host=reinterpret_cast<char *(*)(int,char *)>(dlsym(library,"HarmonyHost"));
 freeGo=reinterpret_cast<void (*)(char *)>(dlsym(library,"HarmonyFree"));
 auto set=reinterpret_cast<void (*)(char *(*)(const char *))>(dlsym(library,"harmony_set_system"));
 if(!host||!freeGo||!set){loadError="Missing native exports";return;}set(SystemCall);
 OH_LOG_Print(LOG_APP, LOG_INFO, 0xFF00, "FnCPNNative", "Go core loaded; native exports resolved");
}
static bool String(napi_env env,napi_value value,std::string &out,size_t limit=256*1024){size_t n=0;if(napi_get_value_string_utf8(env,value,nullptr,0,&n)!=napi_ok||n>limit)return false;out.resize(n+1);napi_get_value_string_utf8(env,value,out.data(),out.size(),&n);out.resize(n);return true;}
static void Dispatch(napi_env env,napi_value callback,void *,void *data){
 std::unique_ptr<std::shared_ptr<Pending>> payload(static_cast<std::shared_ptr<Pending> *>(data));auto item=*payload;
 if(!env)return;
 napi_value argv[2],receiver;
 napi_create_string_utf8(env,item->id.c_str(),NAPI_AUTO_LENGTH,&argv[0]);
 napi_create_string_utf8(env,item->request.c_str(),item->request.size(),&argv[1]);
 napi_get_undefined(env,&receiver);napi_call_function(env,receiver,callback,2,argv,nullptr);
}
static napi_value SetSystem(napi_env env,napi_callback_info info){
 size_t argc=1;napi_value argv[1],name,result;napi_get_cb_info(env,info,&argc,argv,nullptr,nullptr);
 napi_valuetype type;if(argc!=1||napi_typeof(env,argv[0],&type)!=napi_ok||type!=napi_function){napi_throw_type_error(env,nullptr,"System callback required");return nullptr;}
 if(dispatch){napi_throw_error(env,nullptr,"System callback already registered");return nullptr;}
 napi_create_string_utf8(env,"HarmonySystem",NAPI_AUTO_LENGTH,&name);
 if(napi_create_threadsafe_function(env,argv[0],nullptr,name,64,1,nullptr,nullptr,nullptr,Dispatch,&dispatch)!=napi_ok){napi_throw_error(env,nullptr,"Cannot register system callback");return nullptr;}
 napi_unref_threadsafe_function(env,dispatch);napi_get_undefined(env,&result);return result;
}
static napi_value Reply(napi_env env,napi_callback_info info){
 size_t argc=2;napi_value argv[2],result;napi_get_cb_info(env,info,&argc,argv,nullptr,nullptr);std::string id,response;
 if(argc!=2||!String(env,argv[0],id,32)||!String(env,argv[1],response)){napi_throw_type_error(env,nullptr,"Invalid system response");return nullptr;}
 std::shared_ptr<Pending> item;{std::lock_guard<std::mutex> lock(pendingMutex);auto found=pending.find(id);if(found!=pending.end())item=found->second;}
 if(item){std::lock_guard<std::mutex> lock(item->mutex);item->response=response;item->ready=true;item->cv.notify_one();}
 napi_get_undefined(env,&result);return result;
}
struct Job{napi_async_work work;napi_deferred deferred;int action;std::string input,output,error;};
static void Execute(napi_env,void *value){auto job=static_cast<Job *>(value);std::call_once(loaded,Load);if(!loadError.empty()){job->error=loadError;return;}char *out=host(job->action,job->input.data());if(!out){job->error="Empty core response";return;}job->output=out;freeGo(out);}
static void Complete(napi_env env,napi_status status,void *value){auto job=static_cast<Job *>(value);napi_value result;std::string text=job->error.empty()?job->output:job->error;napi_create_string_utf8(env,text.c_str(),text.size(),&result);if(status==napi_ok&&job->error.empty())napi_resolve_deferred(env,job->deferred,result);else napi_reject_deferred(env,job->deferred,result);napi_delete_async_work(env,job->work);delete job;}
static napi_value Invoke(napi_env env,napi_callback_info info){size_t argc=2;napi_value argv[2],promise,name;napi_get_cb_info(env,info,&argc,argv,nullptr,nullptr);auto job=new Job{};if(argc!=2||napi_get_value_int32(env,argv[0],&job->action)!=napi_ok||!String(env,argv[1],job->input)||job->action<0||job->action>5){delete job;napi_throw_type_error(env,nullptr,"Invalid host operation");return nullptr;}napi_create_promise(env,&job->deferred,&promise);napi_create_string_utf8(env,"HarmonyCore",NAPI_AUTO_LENGTH,&name);if(napi_create_async_work(env,nullptr,name,Execute,Complete,job,&job->work)!=napi_ok){delete job;napi_throw_error(env,nullptr,"Cannot create native work");return nullptr;}if(napi_queue_async_work(env,job->work)!=napi_ok){napi_delete_async_work(env,job->work);delete job;napi_throw_error(env,nullptr,"Cannot queue native work");return nullptr;}return promise;}
static void Cleanup(void *) {
 std::lock_guard<std::mutex> guard(pendingMutex);
 if(dispatch){napi_release_threadsafe_function(dispatch,napi_tsfn_abort);dispatch=nullptr;}
 for(auto &entry:pending){auto item=entry.second;std::lock_guard<std::mutex> lock(item->mutex);item->response="{\"version\":1,\"ok\":false,\"error\":\"System context closed\"}";item->ready=true;item->cv.notify_one();}
}
static napi_value Init(napi_env env,napi_value exports){napi_property_descriptor methods[]={{"invoke",nullptr,Invoke,nullptr,nullptr,nullptr,napi_default,nullptr},{"setSystem",nullptr,SetSystem,nullptr,nullptr,nullptr,napi_default,nullptr},{"reply",nullptr,Reply,nullptr,nullptr,nullptr,napi_default,nullptr}};napi_define_properties(env,exports,3,methods);napi_add_env_cleanup_hook(env,Cleanup,nullptr);return exports;}
static napi_module module={1,0,nullptr,Init,"fncpnbridge",nullptr,{0}};
extern "C" __attribute__((constructor)) void Register(){napi_module_register(&module);}
