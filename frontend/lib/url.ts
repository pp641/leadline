/** Reads ?tab= on first render. Safe because app pages only render client-side after auth resolves. */
export function initialTab<T extends string>(tabs: readonly T[], fallback: T): T {
  if (typeof window === "undefined") return fallback;
  const t = new URLSearchParams(window.location.search).get("tab");
  return t && (tabs as readonly string[]).includes(t) ? (t as T) : fallback;
}
