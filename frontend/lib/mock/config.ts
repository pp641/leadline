/**
 * Demo mode: when NEXT_PUBLIC_MOCK_MODE=true (inlined at build time) the app
 * talks to an in-browser mock backend (lib/mock) instead of the Go API.
 */
export const MOCK_MODE = process.env.NEXT_PUBLIC_MOCK_MODE === "true";

export const DEMO_CREDENTIALS = { email: "demo@leadline.test", password: "demo-password-123" };
