// Browsers expose an OS family, not a Linux distribution or CPU architecture.
export function detectDownloadPlatform({userAgentData, platform = '', userAgent = '', maxTouchPoints = 0} = {}) {
  const identity = `${userAgentData?.platform || platform} ${userAgent}`;
  if (/Android/i.test(identity)) return 'android';
  if (/iPhone|iPad|iPod/i.test(identity) || (/Mac/i.test(platform) && maxTouchPoints > 1)) return 'ios';
  if (/Windows|Win32|Win64/i.test(identity)) return 'windows';
  if (/Mac/i.test(identity)) return 'macos';
  if (/Linux|X11/i.test(identity)) return 'linux';
  return null;
}
