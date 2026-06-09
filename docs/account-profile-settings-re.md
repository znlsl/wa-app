# WA account profile settings reverse notes

## Scope

This note covers the post-registration account profile surfaces needed by wa-app:

- account profile picture set/remove;
- account push name set.

It intentionally excludes contact avatar fetching, business cover photo GraphQL flows, and verified-name/business identity claims. Those are different surfaces.

## Profile picture set/remove

Reverse targets:

- `X/7Sb.1.smali`: `SetProfilePhotoProtocolHelper` request and response handler.
- `X/0aV.2.smali`: classic app path `app/sendSetProfilePhoto`.
- `X/C41684IcR.java` / `X/IcR.2.smali`: new profile upload engine calls the same protocol helper.
- `X/08M.smali`: `A09(jid)` confirms the `target` attr is omitted when the target is the current user.
- `X/1kH.2.smali`: server JID singleton resolves to `s.whatsapp.net`.

The normal account-avatar update is a direct IQ in the profile-picture namespace. It does not use the `xwa2_profile_picture_set` GraphQL path; that hit is cover-photo related.

### Set current account picture

For the current account, Android builds:

```xml
<iq xmlns="w:profile:picture" id="<generated>" to="s.whatsapp.net" type="set">
  <picture type="image">...processed image bytes...</picture>
</iq>
```

Notes:

- The helper sends the processed image bytes as binary content of the `picture` node.
- No MIME, width, height, URL, or upload handle is attached by this helper.
- The same helper can target non-self JIDs, such as groups, by adding `target="<jid>"`. Account self updates omit `target`.
- If the caller marks the request as a reupload, the picture node carries `reupload="true"`.

### Remove current account picture

Removal uses the same IQ shape with `picture type="image"` and no binary body. The helper treats a nil image byte slice as delete/remove mode and does not expect a returned picture ID.

### Optional Accounts Center profile-photo sync metadata

When the caller enables profile-photo sync to Accounts Center, the app optionally prepends extra IQ children before `picture`:

```xml
<encryption_metadata version="1" algorithm="<algorithm>">
  <encrypted_key>...</encrypted_key>
  <encrypted_data>...</encrypted_data>
  <auth_tag>...</auth_tag>
  <nonce>...</nonce>
</encryption_metadata>
<fbid>...</fbid>
```

The four binary metadata children are Base64-decoded by the app before being inserted. This is an optional enhancement path: if AC credentials cannot be obtained, Android logs a warning and still sends the normal WA profile-picture IQ without that metadata.

### Success and error response

For set/update with image bytes, Android reads the first response child as `picture` and extracts:

- `id`: new profile-picture ID;
- `has_staging`: string `true` means the result has staging.

For delete/remove, Android reports success without reading a picture ID. Error handling maps the returned IQ error node into a numeric WA error code; no sensitive request body is logged.

## Push name set

Reverse targets:

- `X/Abp.1.smali`: `PushNameSettingHandler` parses and applies push-name mutations.
- `X/Abr.1.smali`: `PushNameSettingMutation` builds the app-state mutation.
- `X/1LZ.smali`: enum `PushNameSetting` has key value `setting_pushName`.
- `X/1La.1.smali` and `X/1Lb.1.smali`: `setting_pushName` maps to app-state collection `critical_block`.
- `X/2P0.smali`: app-state sync-action value contains `pushNameSetting` at field `7`.
- `X/Azj.3.smali`: `PushNameSetting` proto stores `name` at field `1`.
- `X/AWo.smali`: after applying the mutation, Android also sends a live MessageClient message with type `3` and the new name as object.

### Durable app-state mutation

Post-registration name update is an app-state mutation, not a profile-picture IQ.

The mutation model is:

- collection: `critical_block`;
- key path: `['setting_pushName']`;
- operation: set;
- action value: `SyncActionValue.push_name_setting.name = <non-empty name>`.

The relevant proto field numbers from the app are:

```proto
message SyncActionValue {
  int64 timestamp = 1;
  PushNameSetting push_name_setting = 7;
}

message PushNameSetting {
  string name = 1;
}
```

Android accepts the incoming/apply-side mutation only when:

- key array length is exactly one;
- key equals `setting_pushName`;
- operation is set;
- `push_name_setting.name` is present.

When applying, an empty name is logged as invalid and is not treated as a useful account name.

### Live socket side effect

After persisting the name locally, Android calls the message client with `Message.obtain(null, 3, 0, 0, name)`. That path is handed to the native sending channel, so the Java/smali layer does not expose a complete XML node shape for this live side effect.

For wa-app implementation, the safe ordering is:

1. build and send the durable app-state mutation once the syncd/app-state send pipeline is available;
2. if a live push-name send primitive is added later, keep it as an optional best-effort side effect and do not make account state depend on it.

## Implementation implications for wa-app

- Account avatar update can be implemented as a bounded `w:profile:picture` IQ with binary image content and the same timeout/error handling pattern used by contact profile-picture fetches.
- Account avatar remove is the same IQ with empty picture content.
- Accounts Center metadata should remain unsupported/optional unless a caller provides verified AC metadata; failure to obtain it must not fail the WA-only avatar update.
- Push name should be modeled as a first-class account setting action backed by app-state mutation fields, not as a hand-written JSON DTO.
- The registration/login payload also has a push-name field, but that is not the post-registration configuration interface.
- Do not expose raw JIDs, image bytes, app-state blobs, mutation keys, or profile-sync metadata in logs or public API responses.
