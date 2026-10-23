//go:build android

#include <jni.h>
#include <stdlib.h>
#include <string.h>
#include <pthread.h>
#include "_cgo_export.h"

static JavaVM *fncpn_vm;
static jclass fncpn_bridge;
static jmethodID fncpn_rpc;
static pthread_mutex_t fncpn_lock = PTHREAD_MUTEX_INITIALIZER;

static void remember(JNIEnv *env, jclass clazz) {
    pthread_mutex_lock(&fncpn_lock);
    if (!fncpn_bridge) {
        (*env)->GetJavaVM(env, &fncpn_vm);
        fncpn_bridge = (*env)->NewGlobalRef(env, clazz);
        fncpn_rpc = (*env)->GetStaticMethodID(env, clazz, "rpc", "(Ljava/lang/String;)Ljava/lang/String;");
    }
    pthread_mutex_unlock(&fncpn_lock);
}
static char *utf8(JNIEnv *env, jstring text) {
    if (!text) return NULL;
    jclass string_class = (*env)->FindClass(env,"java/lang/String");
    jmethodID get = (*env)->GetMethodID(env,string_class,"getBytes","(Ljava/lang/String;)[B");
    jstring encoding = (*env)->NewStringUTF(env,"UTF-8");
    jbyteArray bytes = (jbyteArray)(*env)->CallObjectMethod(env,text,get,encoding);
    if (!bytes || (*env)->ExceptionCheck(env)) return NULL;
    jsize size=(*env)->GetArrayLength(env,bytes);char *out=malloc(size+1);
    if (out) { (*env)->GetByteArrayRegion(env,bytes,0,size,(jbyte*)out);out[size]=0; }
    (*env)->DeleteLocalRef(env,bytes);(*env)->DeleteLocalRef(env,encoding);(*env)->DeleteLocalRef(env,string_class);
    return out;
}
static jstring java_string(JNIEnv *env,const char *text) {
    jclass clazz=(*env)->FindClass(env,"java/lang/String");
    jmethodID constructor=(*env)->GetMethodID(env,clazz,"<init>","([BLjava/lang/String;)V");
    jbyteArray bytes=(*env)->NewByteArray(env,strlen(text));
    (*env)->SetByteArrayRegion(env,bytes,0,strlen(text),(const jbyte*)text);
    jstring encoding=(*env)->NewStringUTF(env,"UTF-8");
    jstring out=(jstring)(*env)->NewObject(env,clazz,constructor,bytes,encoding);
    (*env)->DeleteLocalRef(env,bytes);(*env)->DeleteLocalRef(env,encoding);(*env)->DeleteLocalRef(env,clazz);return out;
}
char *fncpn_android_rpc(const char *request) {
    pthread_mutex_lock(&fncpn_lock);JavaVM *vm=fncpn_vm;jclass clazz=fncpn_bridge;jmethodID method=fncpn_rpc;pthread_mutex_unlock(&fncpn_lock);
    if (!vm || !clazz || !method) return NULL;
    JNIEnv *env;int detach=0;
    jint status=(*vm)->GetEnv(vm,(void**)&env,JNI_VERSION_1_6);
    if (status==JNI_EDETACHED) { if ((*vm)->AttachCurrentThread(vm,&env,NULL)!=JNI_OK) return NULL;detach=1; }
    else if(status!=JNI_OK)return NULL;
    jstring input=java_string(env,request);
    jstring reply=(jstring)(*env)->CallStaticObjectMethod(env,clazz,method,input);
    char *out=NULL;
    if (!(*env)->ExceptionCheck(env))out=utf8(env,reply);
    if ((*env)->ExceptionCheck(env))(*env)->ExceptionClear(env);
    (*env)->DeleteLocalRef(env,input);if(reply)(*env)->DeleteLocalRef(env,reply);
    if(detach)(*vm)->DetachCurrentThread(vm);return out;
}
JNIEXPORT void JNICALL Java_cn_rectcircle_fncpn_Bridge_prepare(JNIEnv *env,jclass clazz){remember(env,clazz);}
JNIEXPORT jstring JNICALL Java_cn_rectcircle_fncpn_Bridge_start(JNIEnv *env,jclass clazz,jobject owner,jstring directory){
    remember(env,clazz);char *path=utf8(env,directory);if(!path)return (*env)->NewStringUTF(env,"Invalid app directory");
    char *error=fncpnStart(path);free(path);jstring result=java_string(env,error);free(error);return result;
}
JNIEXPORT void JNICALL Java_cn_rectcircle_fncpn_Bridge_stop(JNIEnv *env,jclass clazz,jboolean revoked){fncpnStop(revoked?1:0);}
JNIEXPORT void JNICALL Java_cn_rectcircle_fncpn_Bridge_networkChanged(JNIEnv *env,jclass clazz){fncpnNetworkChanged();}
JNIEXPORT jstring JNICALL Java_cn_rectcircle_fncpn_Bridge_command(JNIEnv *env,jclass clazz,jstring method,jstring params){
    char *m=utf8(env,method),*p=utf8(env,params);if(!m||!p){free(m);free(p);return NULL;}
    char *reply=fncpnCommand(m,p);free(m);free(p);jstring result=java_string(env,reply);free(reply);return result;
}

JNIEXPORT void JNICALL Java_cn_rectcircle_fncpn_Bridge_back(JNIEnv *env,jclass clazz){ fncpnBack(); }
