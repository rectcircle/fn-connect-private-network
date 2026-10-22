package com.rectcircle.fncpn;

import android.content.Context;
import android.content.SharedPreferences;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.Base64;
import org.json.JSONObject;
import java.security.KeyStore;
import java.util.Arrays;
import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;

final class Credentials {
    private static final String ALIAS = "fncpn.credentials.v1";
    private final SharedPreferences preferences;
    Credentials(Context context) { preferences = context.getSharedPreferences("credentials-v1", Context.MODE_PRIVATE); }
    private SecretKey key(boolean create) throws Exception {
        KeyStore store = KeyStore.getInstance("AndroidKeyStore"); store.load(null);
        if (store.containsAlias(ALIAS)) return (SecretKey) store.getKey(ALIAS, null);
        if (!create || !preferences.getAll().isEmpty()) throw new java.security.KeyStoreException("Missing credential key");
        KeyGenerator generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
        generator.init(new KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build());
        return generator.generateKey();
    }
    synchronized Object dispatch(String method, JSONObject params) throws Exception {
        if(method.equals("secret.reset")){
            if(!preferences.edit().clear().commit())throw new Exception("Cannot reset credentials");
            KeyStore store=KeyStore.getInstance("AndroidKeyStore");store.load(null);
            if(store.containsAlias(ALIAS))store.deleteEntry(ALIAS);
            return JSONObject.NULL;
        }
        String name = params.getString("key");
        if (!name.matches("[0-9a-f]{64}")) throw new Exception("Invalid credential identifier");
        if (method.equals("secret.get")) {
            String encoded = preferences.getString(name, null);
            if (encoded == null) return new JSONObject().put("found", false);
            byte[] encrypted = Base64.decode(encoded, Base64.NO_WRAP);
            if (encrypted.length < 28 || encrypted.length > 1048576) throw new java.security.KeyStoreException("Invalid ciphertext");
            Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
            cipher.init(Cipher.DECRYPT_MODE, key(false), new GCMParameterSpec(128, Arrays.copyOf(encrypted, 12)));
            cipher.updateAAD(name.getBytes(java.nio.charset.StandardCharsets.UTF_8));
            return new JSONObject().put("found", true).put("value", Base64.encodeToString(cipher.doFinal(encrypted,12,encrypted.length-12), Base64.NO_WRAP));
        }
        if (method.equals("secret.put")) {
            byte[] value = Base64.decode(params.getString("value"), Base64.NO_WRAP);
            if (value.length > 524288) throw new Exception("Credential is too large");
            Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding"); cipher.init(Cipher.ENCRYPT_MODE, key(true));
            cipher.updateAAD(name.getBytes(java.nio.charset.StandardCharsets.UTF_8));
            byte[] encrypted = cipher.doFinal(value), iv = cipher.getIV();
            byte[] combined = Arrays.copyOf(iv, iv.length+encrypted.length);
            System.arraycopy(encrypted,0,combined,iv.length,encrypted.length); Arrays.fill(value,(byte)0);
            if (!preferences.edit().putString(name,Base64.encodeToString(combined,Base64.NO_WRAP)).commit()) throw new Exception("Cannot save credential");
            return JSONObject.NULL;
        }
        if (method.equals("secret.delete")) {
            if (!preferences.edit().remove(name).commit()) throw new Exception("Cannot delete credential");
            if(preferences.getAll().isEmpty()) {
                KeyStore store=KeyStore.getInstance("AndroidKeyStore");store.load(null);
                if(store.containsAlias(ALIAS))store.deleteEntry(ALIAS);
            }
            return JSONObject.NULL;
        }
        throw new Exception("Unknown credential operation");
    }
}
