// Connection errors are not evidence of an authenticated/configured session.
export function initialLoginPage(state: string, configuredFNID: string): boolean {
  return !configuredFNID || ['UNCONFIGURED', 'AUTH_REQUIRED'].includes(state);
}
