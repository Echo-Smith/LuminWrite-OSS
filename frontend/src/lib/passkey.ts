/**
 * WebAuthn / Passkey 前端工具库
 *
 * 封装 navigator.credentials 的 create/get 调用，
 * 以及与后端 /api/v2/auth/passkey/* 端点的交互。
 */

// ─── Base64 URL 工具 ──────────────────────────────────────

function bufferToBase64URL(buf: ArrayBuffer | Uint8Array): string {
  const bytes = buf instanceof Uint8Array ? buf : new Uint8Array(buf);
  let binary = "";
  for (let i = 0; i < bytes.byteLength; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function base64URLToBuffer(b64url: string): ArrayBuffer {
  const padding = "=".repeat((4 - (b64url.length % 4)) % 4);
  const base64 = (b64url + padding).replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes.buffer;
}

// ─── 检测浏览器支持 ────────────────────────────────────────

export function isWebAuthnSupported(): boolean {
  return typeof window !== "undefined" &&
    "PublicKeyCredential" in window &&
    typeof navigator.credentials !== "undefined";
}

export async function isPlatformAuthenticatorAvailable(): Promise<boolean> {
  if (!isWebAuthnSupported()) return false;
  try {
    return await PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable();
  } catch {
    return false;
  }
}

// ─── 类型定义 ─────────────────────────────────────────────

interface RegistrationChallenge {
  challenge: string;
  user_id: string;
  user_name: string;
  user_display_name: string;
  rp: { name: string; id: string };
  pubKeyCredParams: { type: string; alg: number }[];
  authenticatorSelection: {
    authenticatorAttachment?: string;
    userVerification: string;
    residentKey: string;
  };
  timeout: number;
  attestation: string;
}

interface AuthenticationChallenge {
  challenge: string;
  allowCredentials?: { type: string; id: string; transports?: string[] }[];
  userVerification: string;
  timeout: number;
  rpId: string;
}

// ─── API 调用 ─────────────────────────────────────────────

async function apiCall<T>(url: string, body?: unknown): Promise<T> {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  });
  const json = await res.json();
  if (!json.success) {
    throw new Error(json.error?.message || "API call failed");
  }
  return json.data as T;
}

// ─── Passkey 注册 ─────────────────────────────────────────

export async function registerPasskey(options: {
  name?: string;
  userId?: string;
  userName?: string;
}): Promise<{ success: boolean; message: string }> {
  // 1. 从后端获取 challenge
  const challenge = await apiCall<RegistrationChallenge>(
    "/api/v2/auth/passkey/register/begin",
    {
      name: options.name,
      user_id: options.userId,
      user_name: options.userName,
    }
  );

  // 2. 构造 PublicKeyCredentialCreationOptions
  const publicKeyOptions: PublicKeyCredentialCreationOptions = {
    challenge: base64URLToBuffer(challenge.challenge),
    rp: {
      name: challenge.rp.name,
      id: challenge.rp.id,
    },
    user: {
      id: base64URLToBuffer(challenge.user_id),
      name: challenge.user_name,
      displayName: challenge.user_display_name,
    },
    pubKeyCredParams: challenge.pubKeyCredParams.map((p) => ({
      type: "public-key" as PublicKeyCredentialType,
      alg: p.alg,
    })),
    authenticatorSelection: {
      userVerification: challenge.authenticatorSelection.userVerification as UserVerificationRequirement,
      residentKey: challenge.authenticatorSelection.residentKey as ResidentKeyRequirement,
      ...(challenge.authenticatorSelection.authenticatorAttachment
        ? { authenticatorAttachment: challenge.authenticatorSelection.authenticatorAttachment as AuthenticatorAttachment }
        : {}),
    },
    timeout: challenge.timeout,
    attestation: challenge.attestation as AttestationConveyancePreference,
    excludeCredentials: [],
  };

  // 3. 调用浏览器 API 创建凭据
  const credential = (await navigator.credentials.create({
    publicKey: publicKeyOptions,
  })) as PublicKeyCredential | null;

  if (!credential) {
    throw new Error("Failed to create credential");
  }

  const attestationResponse = credential.response as AuthenticatorAttestationResponse;

  // 4. 将结果发送到后端验证
  const registrationData = {
    id: credential.id,
    rawId: bufferToBase64URL(credential.rawId),
    type: credential.type,
    attestationObject: bufferToBase64URL(attestationResponse.attestationObject),
    clientDataJSON: bufferToBase64URL(attestationResponse.clientDataJSON),
    authenticatorData: attestationResponse.getAuthenticatorData
      ? bufferToBase64URL(attestationResponse.getAuthenticatorData())
      : "",
    publicKey: attestationResponse.getPublicKey
      ? (() => { const pk = attestationResponse.getPublicKey(); return pk ? bufferToBase64URL(pk) : ""; })()
      : "",
    publicKeyAlgorithm: attestationResponse.getPublicKeyAlgorithm
      ? attestationResponse.getPublicKeyAlgorithm()
      : -7,
    transports: attestationResponse.getTransports
      ? attestationResponse.getTransports()
      : [],
  };

  return apiCall<{ success: boolean; message: string }>(
    "/api/v2/auth/passkey/register/complete",
    registrationData
  );
}

// ─── WebAuthn 错误翻译 ────────────────────────────────────

/**
 * 将浏览器 WebAuthn DOMException 转换为用户友好的中文提示
 */
export function getPasskeyErrorMessage(e: unknown): string {
  if (e instanceof DOMException) {
    switch (e.name) {
      case "NotAllowedError":
        // 用户取消 / 超时 / 未授权
        return "Passkey 验证已取消或超时，请重试";
      case "AbortError":
        return "Passkey 验证被中断，请重试";
      case "SecurityError":
        return "当前环境不支持 Passkey，请确保使用 HTTPS 访问";
      case "NotFoundError":
        return "未找到已注册的 Passkey，请先注册或使用密码登录";
      case "InvalidStateError":
        return "设备未配置 Passkey，请先注册";
      default:
        return `Passkey 验证失败：${e.name}`;
    }
  }
  if (e instanceof Error) {
    return e.message || "Passkey 验证失败";
  }
  return "Passkey 验证失败";
}

// ─── Passkey 登录 ─────────────────────────────────────────

export async function loginWithPasskey(options?: {
  userId?: string;
}): Promise<{
  token: string;
  user_id: string;
  username: string;
  role: string;
  expires_in: number;
}> {
  // 1. 从后端获取 challenge
  const challenge = await apiCall<AuthenticationChallenge>(
    "/api/v2/auth/passkey/login/begin",
    { user_id: options?.userId }
  );

  // 2. 构造 PublicKeyCredentialRequestOptions
  const publicKeyOptions: PublicKeyCredentialRequestOptions = {
    challenge: base64URLToBuffer(challenge.challenge),
    rpId: challenge.rpId,
    userVerification: challenge.userVerification as UserVerificationRequirement,
    timeout: challenge.timeout,
    allowCredentials: challenge.allowCredentials?.map((c) => ({
      type: "public-key" as PublicKeyCredentialType,
      id: base64URLToBuffer(c.id),
      transports: c.transports as AuthenticatorTransport[] | undefined,
    })),
  };

  // 3. 调用浏览器 API 获取断言
  const credential = (await navigator.credentials.get({
    publicKey: publicKeyOptions,
  })) as PublicKeyCredential | null;

  if (!credential) {
    throw new Error("Failed to get credential");
  }

  const assertionResponse = credential.response as AuthenticatorAssertionResponse;

  // 4. 将结果发送到后端验证
  const authData = {
    id: credential.id,
    rawId: bufferToBase64URL(credential.rawId),
    type: credential.type,
    authenticatorData: bufferToBase64URL(assertionResponse.authenticatorData),
    clientDataJSON: bufferToBase64URL(assertionResponse.clientDataJSON),
    signature: bufferToBase64URL(assertionResponse.signature),
    userHandle: assertionResponse.userHandle
      ? bufferToBase64URL(assertionResponse.userHandle)
      : undefined,
  };

  return apiCall<{
    token: string;
    user_id: string;
    username: string;
    role: string;
    expires_in: number;
  }>("/api/v2/auth/passkey/login/complete", authData);
}

// ─── Passkey 列表 ─────────────────────────────────────────

export interface PasskeyInfo {
  id: string;
  credential_id: string;
  name: string;
  created_at: string;
  last_used_at?: string;
  transports: string[];
}

export async function listPasskeys(): Promise<PasskeyInfo[]> {
  const res = await fetch("/api/v2/auth/passkey/list");
  const json = await res.json();
  return json.data?.passkeys || [];
}

export async function deletePasskey(id: string): Promise<void> {
  await fetch(`/api/v2/auth/passkey/${id}`, { method: "DELETE" });
}
