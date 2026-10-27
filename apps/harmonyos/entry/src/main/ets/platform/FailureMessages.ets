import { tr } from './Messages';
import { Failure } from './Client';

// User-facing summaries are localized; the original failure stays intact for diagnostics.
export function failureMessage(failure: Failure): string {
  const factors: Record<string, string> = {
    "ACCESS_CODE_REQUIRED": '该 NAS 开启了访问码，请输入访问码', "ACCESS_CODE_INVALID": '访问码不正确，请重试',
    "ACCESS_CODE_RATE_LIMITED": '访问码尝试次数过多，请稍后重试', "TWO_FACTOR_REQUIRED": '该账号需要两步验证，请输入 OTP',
    "TWO_FACTOR_CODE_INVALID": 'OTP 不正确，请重试', "TWO_FACTOR_SETUP_REQUIRED": '该账号要求两步验证，请先在 fnOS 网页端完成绑定'
  };
  if (failure.remoteCode && factors[failure.remoteCode]) { return tr(factors[failure.remoteCode]); }
  if (failure.operation === 'native_session.connect' && ['UNAVAILABLE', 'TIMEOUT'].includes(failure.code)) {
    return tr('服务不可用。请检查网络连接、NAS 上的 FN Connect 是否已启用，以及 FN Connect ID 是否正确。');
  }
  if (failure.code === 'VERSION_INCOMPATIBLE') {
    return tr(failure.upgradeTarget === 'client' ? '版本不兼容，请升级客户端。' : '版本不兼容，请升级服务端。');
  }
  if (failure.operation === 'native_session.authenticate' && failure.code === 'AUTH_REQUIRED' && failure.message === 'fnOS username or password is invalid') {
    return tr('用户名或密码无效，请重试。');
  }
  const inputErrors: Record<string, string> = {
    "invalid FN ID": 'FN Connect ID 格式不正确', "invalid fnOS username": '请输入 fnOS 用户名',
    "invalid fnOS password": '请输入 fnOS 密码', "invalid fnOS access code": '请输入 fnOS 访问码'
  };
  if (failure.code === 'INVALID_ARGUMENT' && inputErrors[failure.message]) { return tr(inputErrors[failure.message]); }
  const summaries: Record<string, string> = {
    "INVALID_ARGUMENT": '请求参数无效，请检查输入。', "AUTH_REQUIRED": '请重新登录后重试。',
    "PERMISSION_DENIED": '权限不足，无法完成操作。', "NOT_FOUND": '目标不存在，请刷新后重试。',
    "ALREADY_EXISTS": '配置冲突，请刷新后重试。', "CONFLICT": '配置冲突，请刷新后重试。',
    "FAILED_PRECONDITION": '操作所需条件尚未满足。', "DEVICE_REVOKED": '设备授权已撤销，请重新登录。',
    "UNAVAILABLE": '服务暂不可用，请稍后重试。', "TIMEOUT": '操作超时，请重试。', "CANCELED": '操作已取消。',
    "INTERNAL": '操作失败，请重试或查看诊断详情。', "PROTOCOL_ERROR": '响应格式无效，请重试。',
    "SERVER_UNAVAILABLE": '未找到 FnCPN 服务，请在 fnOS 安装或启动 FnCPN 后重新连接。',
    "RESOURCE_EXHAUSTED": '资源不足，请稍后重试。', "DISCOVERY_FAILED": '网络地址发现失败，请检查网络。'
  };
  return tr(summaries[failure.code] ?? failure.message);
}

export function dnsStateName(state: string): string {
  const labels: Record<string, string> = { "ready": '就绪', "inactive": '未启用', "degraded": 'DNS 降级', "checking": '正在检测' };
  return tr(labels[state] ?? state);
}
