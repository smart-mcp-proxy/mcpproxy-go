// A seam for leaving the app: the consent page hands the browser back to the
// client's redirect URI, which tests replace with a spy.
export function navigateTo(url: string): void {
  window.location.assign(url)
}
