/** Only store links that can be opened directly in a browser are actionable. */
export function safePurchaseURL(value: string): string | undefined {
  try {
    const parsed = new URL(value)
    return ['https:', 'http:'].includes(parsed.protocol) && !parsed.username && !parsed.password ? parsed.href : undefined
  } catch {
    return undefined
  }
}
