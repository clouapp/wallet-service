# Frontend Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bootstrap the vinext (Next.js 15, Cloudflare Workers) admin panel with the design system copied from `cloubet/front`, routing shell (PublicLayout + DashboardLayout), Redux auth state, landing page, and all auth pages (login, 2FA, password recovery).

**Architecture:** Pages Router (same as cloubet/front). Design system copied directly — `whitelabel.json` color tokens, `globals.css`, Tailwind config with HeroUI plugin. Auth uses JWT (15-min access token stored in memory/Redux, 30-day refresh in httpOnly cookie). All auth pages are under `/login/*`. Meilisearch and Crisp copied but feature-flagged off.

**Prerequisite:** Backend Foundation plan must be deployed or running locally.

**Tech Stack:** Next.js 15, React 19, TypeScript 5, HeroUI 2.x, Tailwind CSS, Redux Toolkit + redux-persist, SWR, Vitest, Playwright.

**Source for copying:** `/Users/raphaelcangucu/projects/cloubet/front/src/`

**Spec:** `docs/superpowers/specs/2026-03-24-admin-panel-design.md`

---

## File Map

```
front/
├── package.json
├── next.config.ts
├── tailwind.config.ts
├── tsconfig.json
├── wrangler.toml                   (Cloudflare Workers config)
├── open-next.config.ts             (OpenNextJS Cloudflare)
├── .env.local
├── .env.example
├── playwright.config.ts
├── vitest.config.ts
│
├── src/
│   ├── pages/
│   │   ├── _app.tsx                (providers stack)
│   │   ├── _document.tsx           (theme script injection)
│   │   ├── index.tsx               (landing page)
│   │   ├── login/
│   │   │   ├── index.tsx           (email+password)
│   │   │   ├── 2fa.tsx             (TOTP code entry)
│   │   │   ├── recover.tsx         (enter email)
│   │   │   └── recover/
│   │   │       └── confirm.tsx     (new password)
│   │   └── dashboard/
│   │       └── index.tsx           (redirect to /dashboard/assets)
│   │
│   ├── layouts/
│   │   ├── PublicLayout.tsx        (landing + auth: no sidebar)
│   │   └── DashboardLayout.tsx     (top nav + sidebar shell)
│   │
│   ├── components/
│   │   ├── Header/
│   │   │   └── Header.tsx          (copied + adapted from cloubet/front)
│   │   ├── Menu/
│   │   │   ├── WebMenu.tsx         (desktop sidebar)
│   │   │   └── MobileMenu.tsx      (hamburger drawer)
│   │   ├── Auth/
│   │   │   ├── LoginForm.tsx
│   │   │   ├── TwoFactorForm.tsx
│   │   │   └── RecoverForm.tsx
│   │   ├── Landing/
│   │   │   ├── HeroSection.tsx
│   │   │   ├── ProblemSection.tsx
│   │   │   ├── SolutionSection.tsx
│   │   │   ├── BenefitsSection.tsx
│   │   │   ├── ProofSection.tsx
│   │   │   ├── ObjectionsSection.tsx
│   │   │   ├── OfferSection.tsx
│   │   │   └── FinalCtaSection.tsx
│   │   └── ui/
│   │       ├── Button/             (copied from cloubet/front)
│   │       ├── Input/              (copied)
│   │       └── CopyButton.tsx      (copy-to-clipboard with icon)
│   │
│   ├── hooks/
│   │   ├── useAuth.ts              (adapted from cloubet/front)
│   │   ├── useAuthSync.ts          (adapted)
│   │   └── useBreakpoints.ts       (copied)
│   │
│   ├── lib/
│   │   ├── api/
│   │   │   └── client.ts           (fetch wrapper: JWT + HMAC headers, SWR error handler)
│   │   └── store/
│   │       ├── index.ts            (Redux store)
│   │       ├── auth.slice.ts       (user, token, current account)
│   │       └── ui.slice.ts         (feature flags, banners)
│   │
│   ├── services/
│   │   ├── providers/
│   │   │   ├── StoreProvider.tsx
│   │   │   └── ThemeProvider.tsx
│   │   ├── crisp/
│   │   │   └── index.ts            (copied, disabled)
│   │   └── meilisearch/
│   │       └── index.ts            (copied, disabled)
│   │
│   ├── styles/
│   │   ├── globals.css             (copied from cloubet/front)
│   │   └── global.ts               (layout class exports)
│   │
│   ├── config/
│   │   └── whitelabel.json         (copied from cloubet/front)
│   │
│   └── utils/
│       ├── whitelabel/             (copied: theme CSS var generation)
│       └── cn.ts                   (tailwind-merge + clsx helper)
│
├── e2e/
│   ├── auth.spec.ts
│   └── helpers/
│       └── setup.ts
│
└── tests/
    └── setup.ts
```

---

## Task 1: Project Bootstrap

**Files:** `package.json`, `next.config.ts`, `tailwind.config.ts`, `tsconfig.json`, `wrangler.toml`, `open-next.config.ts`, `.env.example`

- [ ] **Step 1.1: Scaffold vinext project**

Check if vinext CLI exists:
```bash
npx create-next-app@latest front --typescript --tailwind --no-app --no-src-dir --import-alias "@/*"
```

Then convert to Cloudflare Workers deployment by adding OpenNextJS:
```bash
cd front && npm install @opennextjs/cloudflare wrangler
```

- [ ] **Step 1.2: Install core dependencies**

```bash
cd front && npm install \
  @heroui/react \
  @reduxjs/toolkit react-redux redux-persist \
  swr \
  lucide-react \
  framer-motion \
  react-toastify \
  next-intl \
  dayjs \
  @tailwindcss/container-queries
```

- [ ] **Step 1.3: Install dev dependencies**

```bash
cd front && npm install -D \
  vitest @vitejs/plugin-react jsdom @testing-library/react @testing-library/jest-dom \
  @playwright/test \
  @types/node @types/react @types/react-dom
```

- [ ] **Step 1.4: Install disabled-but-present dependencies**

```bash
cd front && npm install \
  @meilisearch/instant-meilisearch react-instantsearch \
  crisp-sdk-web
```

- [ ] **Step 1.5: Configure Tailwind with HeroUI plugin**

Copy `tailwind.config.ts` from `cloubet/front` and adapt — keep all HeroUI plugin setup, container queries, animations. Remove any betting-specific color names.

```ts
// front/tailwind.config.ts
import { heroui } from "@heroui/react";
import type { Config } from "tailwindcss";

const config: Config = {
  content: [
    "./src/**/*.{js,ts,jsx,tsx,mdx}",
    "./node_modules/@heroui/theme/dist/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      // Copy animation/keyframe extensions from cloubet/front
    },
  },
  darkMode: "class",
  plugins: [
    heroui(),
    require("@tailwindcss/container-queries"),
  ],
};
export default config;
```

- [ ] **Step 1.6: Configure OpenNextJS Cloudflare**

```ts
// front/open-next.config.ts
import type { OpenNextConfig } from "@opennextjs/cloudflare";
const config: OpenNextConfig = { default: { override: { wrapper: "cloudflare-node" } } };
export default config;
```

```toml
# front/wrangler.toml
name = "vault-admin"
main = ".open-next/worker.js"
compatibility_date = "2024-09-23"
compatibility_flags = ["nodejs_compat"]
```

- [ ] **Step 1.7: Configure Vitest**

```ts
// front/vitest.config.ts
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    setupFiles: ["./tests/setup.ts"],
    globals: true,
  },
});
```

- [ ] **Step 1.8: Configure Playwright**

```ts
// front/playwright.config.ts
import { defineConfig, devices } from "@playwright/test";
export default defineConfig({
  testDir: "./e2e",
  baseURL: "http://localhost:3000",
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    { name: "mobile-chrome", use: { ...devices["Pixel 5"] } },
  ],
  webServer: { command: "npm run dev", url: "http://localhost:3000", reuseExistingServer: true },
});
```

- [ ] **Step 1.9: Add .env files**

```bash
# front/.env.example
NEXT_PUBLIC_API_URL=http://localhost:8080
NEXT_PUBLIC_CRISP_ENABLED=false
NEXT_PUBLIC_CRISP_ID=
NEXT_PUBLIC_MEILI_ENABLED=false
NEXT_PUBLIC_MEILI_HOST=
NEXT_PUBLIC_MEILI_API_KEY=
```

```bash
cp front/.env.example front/.env.local
```

- [ ] **Step 1.10: Verify project builds**

```bash
cd front && npm run build 2>&1 | tail -20
```
Expected: build succeeds (no pages yet, just empty project).

- [ ] **Step 1.11: Commit**
```bash
git add front/
git commit -m "feat: bootstrap vinext frontend project"
```

---

## Task 2: Design System

**Files:** `src/styles/globals.css`, `src/styles/global.ts`, `src/config/whitelabel.json`, `src/utils/whitelabel/`

- [ ] **Step 2.1: Write a test that verifies design tokens load**

```ts
// front/tests/design-system.test.ts
import whitelabel from "@/config/whitelabel.json";
describe("whitelabel", () => {
  it("has primary color defined", () => {
    expect(whitelabel.colors.light.primary.default).toBeDefined();
  });
  it("has dark mode colors defined", () => {
    expect(whitelabel.colors.dark.primary.default).toBeDefined();
  });
});
```

- [ ] **Step 2.2: Run test to verify it fails**
```bash
cd front && npm run test -- tests/design-system.test.ts
```
Expected: FAIL (file not found).

- [ ] **Step 2.3: Copy design system files from cloubet/front**

```bash
cp /Users/raphaelcangucu/projects/cloubet/front/src/config/whitelabel.json front/src/config/whitelabel.json
cp /Users/raphaelcangucu/projects/cloubet/front/src/styles/globals.css front/src/styles/globals.css
cp /Users/raphaelcangucu/projects/cloubet/front/src/styles/global.ts front/src/styles/global.ts
cp -r /Users/raphaelcangucu/projects/cloubet/front/src/utils/whitelabel/ front/src/utils/whitelabel/
```

- [ ] **Step 2.4: Add `utils/cn.ts`**

```ts
// front/src/utils/cn.ts
import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
export function cn(...inputs: ClassValue[]) { return twMerge(clsx(inputs)); }
```

- [ ] **Step 2.5: Run test to verify it passes**
```bash
cd front && npm run test -- tests/design-system.test.ts
```
Expected: PASS.

- [ ] **Step 2.6: Commit**
```bash
git add front/src/config/ front/src/styles/ front/src/utils/
git commit -m "feat: copy design system from cloubet/front"
```

---

## Task 3: Redux Store + API Client

**Files:** `src/lib/store/index.ts`, `src/lib/store/auth.slice.ts`, `src/lib/store/ui.slice.ts`, `src/lib/api/client.ts`

- [ ] **Step 3.1: Write failing tests**

```ts
// front/tests/store/auth.slice.test.ts
import { authSlice, login, logout } from "@/lib/store/auth.slice";

describe("auth slice", () => {
  it("starts unauthenticated", () => {
    const state = authSlice.reducer(undefined, { type: "@@INIT" });
    expect(state.isAuthenticated).toBe(false);
    expect(state.user).toBeNull();
  });

  it("sets authenticated on login", () => {
    const state = authSlice.reducer(undefined, login({
      user: { id: "1", email: "a@b.com", full_name: "Test", totp_enabled: false, status: "active" },
      token: "jwt.token.here",
      accountId: "acc-1",
    }));
    expect(state.isAuthenticated).toBe(true);
    expect(state.token).toBe("jwt.token.here");
  });

  it("clears state on logout", () => {
    let state = authSlice.reducer(undefined, login({ user: { id: "1", email: "a@b.com" }, token: "t", accountId: "a" }));
    state = authSlice.reducer(state, logout());
    expect(state.isAuthenticated).toBe(false);
    expect(state.token).toBeNull();
  });
});
```

```ts
// front/tests/api/client.test.ts
import { createApiClient } from "@/lib/api/client";

describe("API client", () => {
  it("attaches Authorization header when token present", async () => {
    let capturedHeaders: HeadersInit = {};
    global.fetch = vi.fn().mockImplementation((_, opts: RequestInit) => {
      capturedHeaders = opts.headers as HeadersInit;
      return Promise.resolve({ ok: true, json: async () => ({}) });
    });
    const client = createApiClient({ token: "my-jwt", accountId: "acc-1" });
    await client.get("/v1/user/me");
    expect((capturedHeaders as Record<string, string>)["Authorization"]).toBe("Bearer my-jwt");
    expect((capturedHeaders as Record<string, string>)["X-Account-Id"]).toBe("acc-1");
  });

  it("throws on 401 response", async () => {
    global.fetch = vi.fn().mockResolvedValue({ ok: false, status: 401, json: async () => ({ error: "unauthorized" }) });
    const client = createApiClient({ token: "expired", accountId: "acc-1" });
    await expect(client.get("/v1/user/me")).rejects.toThrow("unauthorized");
  });
});
```

- [ ] **Step 3.2: Run tests to verify they fail**
```bash
cd front && npm run test 2>&1 | head -15
```

- [ ] **Step 3.3: Implement auth slice**

```ts
// front/src/lib/store/auth.slice.ts
import { createSlice, PayloadAction } from "@reduxjs/toolkit";

interface User { id: string; email: string; full_name?: string; totp_enabled: boolean; status: string; }
interface AuthState {
  user: User | null;
  token: string | null;
  accountId: string | null;
  isAuthenticated: boolean;
}

const initialState: AuthState = { user: null, token: null, accountId: null, isAuthenticated: false };

export const authSlice = createSlice({
  name: "auth",
  initialState,
  reducers: {
    login(state, action: PayloadAction<{ user: User; token: string; accountId: string }>) {
      state.user = action.payload.user;
      state.token = action.payload.token;
      state.accountId = action.payload.accountId;
      state.isAuthenticated = true;
    },
    logout(state) { return initialState; },
    setToken(state, action: PayloadAction<string>) { state.token = action.payload; },
    setAccountId(state, action: PayloadAction<string>) { state.accountId = action.payload; },
  },
});

export const { login, logout, setToken, setAccountId } = authSlice.actions;
export default authSlice.reducer;
```

- [ ] **Step 3.4: Implement ui slice**

```ts
// front/src/lib/store/ui.slice.ts
import { createSlice, PayloadAction } from "@reduxjs/toolkit";

interface UIState {
  dismissedBanners: string[];
  features: { meilisearch: boolean; crisp: boolean; };
}

const initialState: UIState = {
  dismissedBanners: [],
  features: {
    meilisearch: process.env.NEXT_PUBLIC_MEILI_ENABLED === "true",
    crisp: process.env.NEXT_PUBLIC_CRISP_ENABLED === "true",
  },
};

export const uiSlice = createSlice({
  name: "ui",
  initialState,
  reducers: {
    dismissBanner(state, action: PayloadAction<string>) {
      state.dismissedBanners.push(action.payload);
    },
  },
});

export const { dismissBanner } = uiSlice.actions;
export default uiSlice.reducer;
```

- [ ] **Step 3.5: Implement Redux store with persist**

```ts
// front/src/lib/store/index.ts
import { configureStore } from "@reduxjs/toolkit";
import { persistStore, persistReducer } from "redux-persist";
import storage from "redux-persist/lib/storage";
import authReducer from "./auth.slice";
import uiReducer from "./ui.slice";

const authPersistConfig = { key: "auth", storage, whitelist: ["user", "token", "accountId", "isAuthenticated"] };

export const store = configureStore({
  reducer: {
    auth: persistReducer(authPersistConfig, authReducer),
    ui: uiReducer,
  },
  middleware: (getDefaultMiddleware) =>
    getDefaultMiddleware({ serializableCheck: { ignoredActions: ["persist/PERSIST", "persist/REHYDRATE"] } }),
});

export const persistor = persistStore(store);
export type RootState = ReturnType<typeof store.getState>;
export type AppDispatch = typeof store.dispatch;
```

- [ ] **Step 3.6: Implement API client**

```ts
// front/src/lib/api/client.ts
interface ClientConfig { token?: string | null; accountId?: string | null; }

class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

export function createApiClient(config: ClientConfig) {
  const baseURL = process.env.NEXT_PUBLIC_API_URL || "";

  async function request(method: string, path: string, body?: unknown) {
    const headers: Record<string, string> = { "Content-Type": "application/json" };
    if (config.token) headers["Authorization"] = `Bearer ${config.token}`;
    if (config.accountId) headers["X-Account-Id"] = config.accountId;

    const res = await fetch(`${baseURL}${path}`, {
      method,
      headers,
      body: body ? JSON.stringify(body) : undefined,
      credentials: "include", // send refresh token cookie
    });

    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: res.statusText }));
      throw new ApiError(res.status, err.error || "Request failed");
    }

    return res.status === 204 ? null : res.json();
  }

  return {
    get: (path: string) => request("GET", path),
    post: (path: string, body?: unknown) => request("POST", path, body),
    put: (path: string, body?: unknown) => request("PUT", path, body),
    delete: (path: string) => request("DELETE", path),
    patch: (path: string, body?: unknown) => request("PATCH", path, body),
  };
}
```

- [ ] **Step 3.7: Run tests to verify they pass**
```bash
cd front && npm run test
```
Expected: all PASS.

- [ ] **Step 3.8: Commit**
```bash
git add front/src/lib/
git commit -m "feat: add Redux store (auth, ui slices) and API client"
```

---

## Task 4: useAuth Hook

**Files:** `src/hooks/useAuth.ts`, `src/hooks/useAuthSync.ts`

- [ ] **Step 4.1: Write failing tests**

```ts
// front/tests/hooks/useAuth.test.ts
import { renderHook, act } from "@testing-library/react";
import { useAuth } from "@/hooks/useAuth";
import { Provider } from "react-redux";
import { store } from "@/lib/store";

const wrapper = ({ children }: { children: React.ReactNode }) => (
  <Provider store={store}>{children}</Provider>
);

describe("useAuth", () => {
  it("starts unauthenticated", () => {
    const { result } = renderHook(() => useAuth(), { wrapper });
    expect(result.current.isAuthenticated).toBe(false);
  });

  it("isAuthenticated after login()", () => {
    const { result } = renderHook(() => useAuth(), { wrapper });
    act(() => {
      result.current.login(
        { id: "1", email: "a@b.com", full_name: "Test", totp_enabled: false, status: "active" },
        "token123",
        "acc-1"
      );
    });
    expect(result.current.isAuthenticated).toBe(true);
    expect(result.current.user?.email).toBe("a@b.com");
  });

  it("not authenticated after logout()", () => {
    const { result } = renderHook(() => useAuth(), { wrapper });
    act(() => result.current.login({ id: "1", email: "a@b.com", totp_enabled: false, status: "active" }, "t", "a"));
    act(() => result.current.logout());
    expect(result.current.isAuthenticated).toBe(false);
  });
});
```

- [ ] **Step 4.2: Run tests to verify they fail**

- [ ] **Step 4.3: Implement useAuth**

```ts
// front/src/hooks/useAuth.ts
import { useDispatch, useSelector } from "react-redux";
import { useRouter } from "next/router";
import { login as loginAction, logout as logoutAction, setAccountId } from "@/lib/store/auth.slice";
import type { RootState } from "@/lib/store";

interface User { id: string; email: string; full_name?: string; totp_enabled: boolean; status: string; }

export function useAuth() {
  const dispatch = useDispatch();
  const router = useRouter();
  const { user, token, accountId, isAuthenticated } = useSelector((s: RootState) => s.auth);

  function login(user: User, token: string, accountId: string) {
    dispatch(loginAction({ user, token, accountId }));
  }

  function logout() {
    dispatch(logoutAction());
    // Optionally call POST /auth/logout to revoke refresh token
    fetch(`${process.env.NEXT_PUBLIC_API_URL}/auth/logout`, { method: "POST", credentials: "include" });
    router.push("/login");
  }

  function switchAccount(newAccountId: string) {
    dispatch(setAccountId(newAccountId));
  }

  return { user, token, accountId, isAuthenticated, login, logout, switchAccount };
}
```

- [ ] **Step 4.4: Run tests to verify they pass**
```bash
cd front && npm run test -- tests/hooks/useAuth.test.ts
```

- [ ] **Step 4.5: Commit**
```bash
git add front/src/hooks/
git commit -m "feat: add useAuth hook"
```

---

## Task 5: Layouts + App Shell

**Files:** `src/pages/_app.tsx`, `src/pages/_document.tsx`, `src/layouts/PublicLayout.tsx`, `src/layouts/DashboardLayout.tsx`, `src/components/Header/Header.tsx`, `src/components/Menu/WebMenu.tsx`, `src/components/Menu/MobileMenu.tsx`

- [ ] **Step 5.1: Copy + adapt Header from cloubet/front**

```bash
cp /Users/raphaelcangucu/projects/cloubet/front/src/components/Header/Header.tsx front/src/components/Header/Header.tsx
```

Then adapt — remove: wallet/currency selector, game-specific links, Pusher real-time. Keep: logo, profile button, notifications bell, account switcher (empty stub for now).

- [ ] **Step 5.2: Copy + adapt Menu from cloubet/front**

```bash
cp /Users/raphaelcangucu/projects/cloubet/front/src/components/Menu/WebMenu.tsx front/src/components/Menu/WebMenu.tsx
cp /Users/raphaelcangucu/projects/cloubet/front/src/components/Menu/MobileMenu.tsx front/src/components/Menu/MobileMenu.tsx
```

Adapt — simplify nav items to:
- Assets (`/dashboard/assets`) — only top-level nav item
- Enterprise Settings (`/dashboard/settings`) — in bottom/secondary nav

- [ ] **Step 5.3: Create PublicLayout**

```tsx
// front/src/layouts/PublicLayout.tsx
import type { ReactNode } from "react";

export default function PublicLayout({ children }: { children: ReactNode }) {
  return (
    <div className="min-h-screen bg-background">
      {children}
    </div>
  );
}
```

- [ ] **Step 5.4: Create DashboardLayout**

```tsx
// front/src/layouts/DashboardLayout.tsx
import { useState } from "react";
import Header from "@/components/Header/Header";
import WebMenu from "@/components/Menu/WebMenu";
import MobileMenu from "@/components/Menu/MobileMenu";

export default function DashboardLayout({ children }: { children: ReactNode }) {
  const [mobileOpen, setMobileOpen] = useState(false);
  return (
    <div className="flex h-screen bg-background">
      {/* Sidebar — hidden on mobile */}
      <aside className="hidden md:flex w-60 flex-shrink-0">
        <WebMenu />
      </aside>
      {/* Mobile drawer */}
      <MobileMenu open={mobileOpen} onClose={() => setMobileOpen(false)} />
      {/* Main content */}
      <div className="flex flex-1 flex-col overflow-hidden">
        <Header onMenuToggle={() => setMobileOpen(true)} />
        <main className="flex-1 overflow-y-auto p-4 md:p-6">
          {children}
        </main>
      </div>
    </div>
  );
}
```

- [ ] **Step 5.5: Create `_app.tsx`**

```tsx
// front/src/pages/_app.tsx
import "@/styles/globals.css";
import type { AppProps } from "next/app";
import { Provider } from "react-redux";
import { PersistGate } from "redux-persist/integration/react";
import { HeroUIProvider } from "@heroui/react";
import { ToastContainer } from "react-toastify";
import "react-toastify/dist/ReactToastify.css";
import { store, persistor } from "@/lib/store";
import { applyTheme } from "@/utils/whitelabel";

// Apply CSS variables from whitelabel config on load
if (typeof window !== "undefined") { applyTheme(); }

export default function App({ Component, pageProps }: AppProps) {
  // Layouts are applied per-page via getLayout pattern
  const getLayout = (Component as any).getLayout ?? ((page: React.ReactNode) => page);

  return (
    <Provider store={store}>
      <PersistGate loading={null} persistor={persistor}>
        <HeroUIProvider>
          {getLayout(<Component {...pageProps} />)}
          <ToastContainer position="bottom-right" theme="dark" />
        </HeroUIProvider>
      </PersistGate>
    </Provider>
  );
}
```

- [ ] **Step 5.6: Create `_document.tsx`** — inject theme script to prevent flash of wrong theme:

```tsx
// front/src/pages/_document.tsx
import { Html, Head, Main, NextScript } from "next/document";
export default function Document() {
  return (
    <Html lang="en">
      <Head />
      <body>
        <Main />
        <NextScript />
      </body>
    </Html>
  );
}
```

- [ ] **Step 5.7: Verify project compiles**
```bash
cd front && npm run dev 2>&1 | head -20
```
Expected: server starts on port 3000.

- [ ] **Step 5.8: Commit**
```bash
git add front/src/pages/ front/src/layouts/ front/src/components/Header/ front/src/components/Menu/
git commit -m "feat: add app shell (layouts, header, menu, providers)"
```

---

## Task 6: Landing Page

**Files:**
- `src/pages/index.tsx`
- `src/components/Landing/HeroSection.tsx`
- `src/components/Landing/ProblemSection.tsx`
- `src/components/Landing/SolutionSection.tsx`
- `src/components/Landing/BenefitsSection.tsx`
- `src/components/Landing/ProofSection.tsx`
- `src/components/Landing/ObjectionsSection.tsx`
- `src/components/Landing/OfferSection.tsx`
- `src/components/Landing/FinalCtaSection.tsx`

**Audience:** Solution-aware — CTOs, engineering leads, and founders at crypto companies/fintechs who know custody-as-a-service exists and are comparing options. Lead with differentiation, not education.

- [ ] **Step 6.1: Write E2E test**

```ts
// front/e2e/landing.spec.ts
import { test, expect } from "@playwright/test";

test("landing page loads with hero headline and primary CTA", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Secure Multi-Chain Crypto Custody");
  await expect(page.getByRole("link", { name: "Start for Free" })).toBeVisible();
});

test("landing page shows all major sections", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("problem-section")).toBeVisible();
  await expect(page.getByTestId("benefits-section")).toBeVisible();
  await expect(page.getByTestId("objections-section")).toBeVisible();
  await expect(page.getByTestId("final-cta-section")).toBeVisible();
});

test("landing page is mobile responsive", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect(page.getByRole("link", { name: "Start for Free" })).toBeVisible();
});

test("login and signup CTAs navigate to /login", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("link", { name: "Start for Free" }).first().click();
  await expect(page).toHaveURL("/login");
});
```

- [ ] **Step 6.2: Run E2E test to verify it fails**
```bash
cd front && npx playwright test e2e/landing.spec.ts --reporter=line 2>&1 | tail -10
```

- [ ] **Step 6.3: Create HeroSection**

```tsx
// front/src/components/Landing/HeroSection.tsx
import Link from "next/link";
import { Button } from "@heroui/react";

export function HeroSection() {
  return (
    <section className="flex flex-col items-center justify-center min-h-[90vh] px-4 text-center bg-gradient-to-b from-background to-content2">
      {/* Badge */}
      <span className="inline-flex items-center gap-2 px-3 py-1 rounded-full text-xs font-medium bg-primary/10 text-primary border border-primary/20 mb-8">
        Early Access — No enterprise contract required
      </span>

      {/* Headline: 8-12 words, matches solution-aware audience */}
      <h1 className="text-4xl sm:text-5xl md:text-6xl font-bold text-foreground max-w-4xl mb-6 leading-tight">
        Secure Multi-Chain Crypto Custody.{" "}
        <span className="text-primary">Live in Minutes.</span>
      </h1>

      {/* Subheadline: 15-20 words, expands on promise */}
      <p className="text-lg md:text-xl text-default-500 max-w-2xl mb-10 leading-relaxed">
        Stop rebuilding key management from scratch. Enterprise-grade custody for BTC, ETH, SOL and more — without the six-figure contract.
      </p>

      {/* CTAs */}
      <div className="flex flex-col sm:flex-row gap-4">
        <Button
          as={Link}
          href="/login"
          color="primary"
          size="lg"
          className="font-semibold px-8"
        >
          Start for Free
        </Button>
        <Button
          as={Link}
          href="/login"
          variant="bordered"
          size="lg"
          className="font-medium px-8"
        >
          Sign In
        </Button>
      </div>

      {/* Social proof line */}
      <p className="mt-8 text-sm text-default-400">
        Self-hosted on your AWS account. Your keys, your infrastructure.
      </p>
    </section>
  );
}
```

- [ ] **Step 6.4: Create ProblemSection**

```tsx
// front/src/components/Landing/ProblemSection.tsx
export function ProblemSection() {
  return (
    <section data-testid="problem-section" className="py-20 px-4 bg-content1">
      <div className="max-w-3xl mx-auto text-center">
        <h2 className="text-2xl md:text-3xl font-bold text-foreground mb-6">
          Your team shouldn't be building custody infrastructure.
        </h2>
        <p className="text-default-500 text-lg leading-relaxed mb-4">
          Yet here you are: duct-taping together cold storage solutions, manually tracking withdrawals, and praying nothing breaks on a Saturday night.
        </p>
        <p className="text-default-500 text-lg leading-relaxed">
          Every hour your engineering team spends on key management plumbing is an hour not spent on your actual product. Existing enterprise solutions cost a fortune and take months to onboard. Building in-house takes longer and requires security expertise most teams don't have.
        </p>
      </div>
    </section>
  );
}
```

- [ ] **Step 6.5: Create SolutionSection**

```tsx
// front/src/components/Landing/SolutionSection.tsx
export function SolutionSection() {
  return (
    <section className="py-20 px-4">
      <div className="max-w-3xl mx-auto text-center">
        <p className="text-primary font-semibold uppercase tracking-wide text-sm mb-4">
          The Solution
        </p>
        <h2 className="text-2xl md:text-3xl font-bold text-foreground mb-6">
          A custody API that runs on your infrastructure, under your control.
        </h2>
        <p className="text-default-500 text-lg leading-relaxed">
          Vault is a self-hosted, multi-chain custody service built for teams that need production-grade security without BitGo's sales cycle. Connect your wallets, assign roles to your team, set withdrawal rules, and go live — all through a clean REST API and a purpose-built admin panel. Runs on your AWS account. Your keys never leave your environment.
        </p>
      </div>
    </section>
  );
}
```

- [ ] **Step 6.6: Create BenefitsSection**

```tsx
// front/src/components/Landing/BenefitsSection.tsx
const benefits = [
  {
    icon: "🔐",
    title: "Full custody, zero infrastructure guesswork",
    detail:
      "Vault runs on your own AWS account. Your keys never leave your environment. No shared infrastructure, no third-party custody risk.",
  },
  {
    icon: "⛓️",
    title: "Multi-chain from day one",
    detail:
      "BTC, ETH, LTC, DOGE, SOL and ERC-20 tokens. One API, one admin panel, every chain your product needs.",
  },
  {
    icon: "👥",
    title: "Granular team controls",
    detail:
      "Create accounts, assign roles (owner, admin, auditor), and scope wallet permissions per user. Auditors see everything. Spend-only users can withdraw — nothing more.",
  },
  {
    icon: "⚡",
    title: "Programmatic + human workflows",
    detail:
      "HMAC access tokens for your backend services. JWT sessions for your ops team. Whitelist addresses, set fee policies, and manage webhooks — all without touching code.",
  },
  {
    icon: "🛡️",
    title: "Withdrawal confidence",
    detail:
      "Whitelist-only destinations, per-wallet freeze controls, and a full transaction timeline for every movement. Know exactly what happened, who approved it, and when.",
  },
];

export function BenefitsSection() {
  return (
    <section data-testid="benefits-section" className="py-20 px-4 bg-content1">
      <div className="max-w-5xl mx-auto">
        <p className="text-primary font-semibold uppercase tracking-wide text-sm text-center mb-4">
          Why Vault
        </p>
        <h2 className="text-2xl md:text-3xl font-bold text-foreground text-center mb-12">
          Everything your custody layer needs. Nothing it doesn't.
        </h2>
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
          {benefits.map((b) => (
            <div
              key={b.title}
              className="p-6 rounded-xl border border-divider bg-background hover:border-primary/40 transition-colors"
            >
              <span className="text-3xl mb-4 block">{b.icon}</span>
              <h3 className="text-base font-semibold text-foreground mb-2">{b.title}</h3>
              <p className="text-sm text-default-500 leading-relaxed">{b.detail}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 6.7: Create ProofSection**

```tsx
// front/src/components/Landing/ProofSection.tsx
const trustStats = [
  { value: "AES-256-GCM", label: "Encryption at rest" },
  { value: "TOTP 2FA", label: "On every account" },
  { value: "HMAC-SHA256", label: "API authentication" },
  { value: "Full audit trail", label: "Every wallet action" },
];

const testimonial = {
  quote:
    "We replaced a custom custody layer we'd been maintaining for two years. Vault's API matched our existing integration in a weekend.",
  author: "CTO, crypto fintech",
  note: "Early access customer",
};

export function ProofSection() {
  return (
    <section className="py-20 px-4">
      <div className="max-w-5xl mx-auto">
        {/* Stats */}
        <div className="grid grid-cols-2 md:grid-cols-4 gap-6 mb-16">
          {trustStats.map((s) => (
            <div key={s.label} className="text-center">
              <p className="text-xl font-bold text-primary mb-1">{s.value}</p>
              <p className="text-sm text-default-500">{s.label}</p>
            </div>
          ))}
        </div>

        {/* Testimonial */}
        <div className="max-w-2xl mx-auto p-8 rounded-2xl border border-divider bg-content1 text-center">
          <p className="text-lg text-foreground italic mb-6">"{testimonial.quote}"</p>
          <p className="text-sm font-semibold text-foreground">{testimonial.author}</p>
          <p className="text-xs text-default-400">{testimonial.note}</p>
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 6.8: Create ObjectionsSection**

```tsx
// front/src/components/Landing/ObjectionsSection.tsx
const faqs = [
  {
    q: "Is this secure enough for production?",
    a: "Vault uses the same custody primitives as enterprise services: encrypted key storage, HMAC-signed API calls, address whitelisting, and role-based access. You control the infrastructure — nothing runs on shared servers.",
  },
  {
    q: "We're already on BitGo / Fireblocks. Why switch?",
    a: "If you're paying for seats you don't use or waiting on an account manager to add a chain, Vault removes the middleman. Self-hosted means you move at your speed, not theirs.",
  },
  {
    q: "What if we need support?",
    a: "Vault ships with full Swagger docs, a typed REST API, and an admin panel your ops team can use without filing tickets. For teams that want implementation help, paid onboarding is available.",
  },
];

export function ObjectionsSection() {
  return (
    <section data-testid="objections-section" className="py-20 px-4 bg-content1">
      <div className="max-w-3xl mx-auto">
        <h2 className="text-2xl md:text-3xl font-bold text-foreground text-center mb-12">
          Common questions
        </h2>
        <div className="space-y-6">
          {faqs.map((faq) => (
            <div key={faq.q} className="p-6 rounded-xl border border-divider bg-background">
              <h3 className="font-semibold text-foreground mb-2">{faq.q}</h3>
              <p className="text-default-500 text-sm leading-relaxed">{faq.a}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 6.9: Create OfferSection**

```tsx
// front/src/components/Landing/OfferSection.tsx
import Link from "next/link";
import { Button, Chip } from "@heroui/react";

const included = [
  "Multi-chain custody API (BTC, ETH, SOL, LTC, DOGE + ERC-20)",
  "Admin panel with full wallet management",
  "Role-based access — accounts → wallets → users",
  "Address whitelisting + per-wallet withdrawal controls",
  "Webhook notifications for every transaction event",
  "TOTP 2FA + programmatic HMAC access tokens",
  "Full Swagger / OpenAPI documentation",
];

export function OfferSection() {
  return (
    <section className="py-20 px-4">
      <div className="max-w-2xl mx-auto text-center">
        <p className="text-primary font-semibold uppercase tracking-wide text-sm mb-4">
          What You Get
        </p>
        <h2 className="text-2xl md:text-3xl font-bold text-foreground mb-8">
          Everything included. Bring your own AWS.
        </h2>

        <ul className="text-left space-y-3 mb-10">
          {included.map((item) => (
            <li key={item} className="flex items-start gap-3 text-sm text-default-600">
              <span className="text-primary mt-0.5">✓</span>
              {item}
            </li>
          ))}
        </ul>

        <div className="p-6 rounded-2xl border border-primary/30 bg-primary/5 mb-6">
          <p className="text-lg font-semibold text-foreground mb-1">
            Free during early access
          </p>
          <p className="text-sm text-default-500">
            No per-seat fees. No enterprise contract. Run it on your own infrastructure.
          </p>
        </div>

        <div className="flex items-center justify-center gap-2 text-sm text-default-400">
          <Chip size="sm" variant="flat" color="success">30-day guarantee</Chip>
          <span>If Vault doesn't fit your stack, we'll help you migrate your data out.</span>
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 6.10: Create FinalCtaSection**

```tsx
// front/src/components/Landing/FinalCtaSection.tsx
import Link from "next/link";
import { Button } from "@heroui/react";

export function FinalCtaSection() {
  return (
    <section
      data-testid="final-cta-section"
      className="py-24 px-4 bg-gradient-to-t from-background to-content2"
    >
      <div className="max-w-2xl mx-auto text-center">
        <h2 className="text-3xl md:text-4xl font-bold text-foreground mb-4">
          Your custody layer, under your control.
        </h2>
        <p className="text-default-500 text-lg mb-10">
          Join early access — no enterprise contract, no sales call required.
        </p>
        <div className="flex flex-col sm:flex-row gap-4 justify-center">
          <Button
            as={Link}
            href="/login"
            color="primary"
            size="lg"
            className="font-semibold px-10"
          >
            Create Your Account
          </Button>
        </div>
        <p className="mt-6 text-xs text-default-400">
          No credit card required. 30-day money-back guarantee.
        </p>
      </div>
    </section>
  );
}
```

- [ ] **Step 6.11: Create landing page — compose all sections**

```tsx
// front/src/pages/index.tsx
import PublicLayout from "@/layouts/PublicLayout";
import { HeroSection } from "@/components/Landing/HeroSection";
import { ProblemSection } from "@/components/Landing/ProblemSection";
import { SolutionSection } from "@/components/Landing/SolutionSection";
import { BenefitsSection } from "@/components/Landing/BenefitsSection";
import { ProofSection } from "@/components/Landing/ProofSection";
import { ObjectionsSection } from "@/components/Landing/ObjectionsSection";
import { OfferSection } from "@/components/Landing/OfferSection";
import { FinalCtaSection } from "@/components/Landing/FinalCtaSection";

export default function LandingPage() {
  return (
    <>
      <HeroSection />
      <ProblemSection />
      <SolutionSection />
      <BenefitsSection />
      <ProofSection />
      <ObjectionsSection />
      <OfferSection />
      <FinalCtaSection />
    </>
  );
}

LandingPage.getLayout = (page: React.ReactNode) => (
  <PublicLayout>{page}</PublicLayout>
);
```

- [ ] **Step 6.12: Run E2E test to verify it passes**
```bash
cd front && npx playwright test e2e/landing.spec.ts --reporter=line
```
Expected: PASS.

- [ ] **Step 6.13: Commit**
```bash
git add front/src/pages/index.tsx front/src/components/Landing/ front/e2e/landing.spec.ts
git commit -m "feat: add landing page with full copy (hero, problem, benefits, objections, CTA)"
```

---

## Task 7: Auth Pages (Login, 2FA, Recovery)

**Files:** `src/pages/login/index.tsx`, `src/pages/login/2fa.tsx`, `src/pages/login/recover.tsx`, `src/pages/login/recover/confirm.tsx`, `src/components/Auth/*.tsx`

- [ ] **Step 7.1: Write E2E tests**

```ts
// front/e2e/auth.spec.ts
import { test, expect } from "@playwright/test";

test("login page renders form", async ({ page }) => {
  await page.goto("/login");
  await expect(page.getByLabel("Email Address")).toBeVisible();
  await expect(page.getByLabel("Password")).toBeVisible();
  await expect(page.getByRole("button", { name: /log in/i })).toBeVisible();
});

test("login with wrong password shows error", async ({ page }) => {
  await page.goto("/login");
  await page.fill('[name="email"]', "wrong@test.com");
  await page.fill('[name="password"]', "wrongpassword");
  await page.click('button[type="submit"]');
  await expect(page.getByText(/invalid credentials/i)).toBeVisible({ timeout: 5000 });
});

test("2fa page renders code input", async ({ page }) => {
  await page.goto("/login/2fa");
  await expect(page.getByLabel(/code/i)).toBeVisible();
});

test("recover page renders email input", async ({ page }) => {
  await page.goto("/login/recover");
  await expect(page.getByLabel(/email/i)).toBeVisible();
});
```

- [ ] **Step 7.2: Run E2E tests to verify they fail**
```bash
cd front && npx playwright test e2e/auth.spec.ts --reporter=line 2>&1 | tail -10
```

- [ ] **Step 7.3: Create LoginForm component**

```tsx
// front/src/components/Auth/LoginForm.tsx
import { useState } from "react";
import { Input, Button } from "@heroui/react";
import Link from "next/link";
import { useRouter } from "next/router";
import { useAuth } from "@/hooks/useAuth";
import { createApiClient } from "@/lib/api/client";
import { toast } from "react-toastify";

export function LoginForm() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const { login } = useAuth();
  const router = useRouter();
  const client = createApiClient({});

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setLoading(true);
    try {
      const res = await client.post("/auth/login", { email, password });
      if (res.requires_2fa) {
        // Store user_id for 2FA step
        sessionStorage.setItem("2fa_user_id", res.user_id);
        router.push("/login/2fa");
        return;
      }
      // Fetch user profile + accounts
      const meClient = createApiClient({ token: res.token });
      const [me, accounts] = await Promise.all([
        meClient.get("/v1/user/me"),
        meClient.get("/v1/user/me/accounts"),
      ]);
      const defaultAccount = accounts.data?.[0];
      login(me, res.token, defaultAccount?.id ?? "");
      router.push("/dashboard/assets");
    } catch (err: any) {
      toast.error(err.message || "Invalid credentials");
    } finally {
      setLoading(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-4 w-full max-w-sm">
      <h1 className="text-3xl font-bold text-title">Welcome Back</h1>
      <Input
        label="Email Address"
        type="email"
        name="email"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        required
      />
      <Input
        label="Password"
        type="password"
        name="password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        required
      />
      <Link href="/login/recover" className="text-sm text-primary self-start">
        Forgot Password?
      </Link>
      <Button type="submit" color="primary" isLoading={loading} fullWidth>
        Log in
      </Button>
    </form>
  );
}
```

- [ ] **Step 7.4: Create TwoFactorForm component**

```tsx
// front/src/components/Auth/TwoFactorForm.tsx
// 6-digit TOTP code input
// On submit: POST /auth/2fa/verify with { user_id, code }
// On success: same login flow as LoginForm (get token, fetch me + accounts)
// "Use recovery code instead" link
```

- [ ] **Step 7.5: Create RecoverForm component**

```tsx
// front/src/components/Auth/RecoverForm.tsx
// Email input → POST /auth/recover → show "Check your email" confirmation
```

- [ ] **Step 7.6: Create RecoverConfirmForm component**

```tsx
// front/src/components/Auth/RecoverConfirmForm.tsx
// New password + confirm → POST /auth/recover/confirm with token from URL query param
// On success: redirect to /login
```

- [ ] **Step 7.7: Create page files**

```tsx
// front/src/pages/login/index.tsx
import PublicLayout from "@/layouts/PublicLayout";
import { LoginForm } from "@/components/Auth/LoginForm";

export default function LoginPage() {
  return (
    <div className="flex min-h-screen items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <LoginForm />
      </div>
    </div>
  );
}
LoginPage.getLayout = (page: React.ReactNode) => <PublicLayout>{page}</PublicLayout>;
```

Same pattern for `login/2fa.tsx`, `login/recover.tsx`, `login/recover/confirm.tsx`.

- [ ] **Step 7.8: Create dashboard redirect**

```tsx
// front/src/pages/dashboard/index.tsx
import { useEffect } from "react";
import { useRouter } from "next/router";
export default function DashboardIndex() {
  const router = useRouter();
  useEffect(() => { router.replace("/dashboard/assets"); }, []);
  return null;
}
```

- [ ] **Step 7.9: Run E2E tests to verify they pass**
```bash
cd front && npx playwright test e2e/auth.spec.ts --reporter=line
```
Expected: all PASS.

- [ ] **Step 7.10: Commit**
```bash
git add front/src/pages/login/ front/src/pages/dashboard/ front/src/components/Auth/ front/e2e/auth.spec.ts
git commit -m "feat: add login, 2FA, and password recovery pages"
```

---

## Task 8: Copy UI Primitives + Disabled Services

**Files:** `src/components/ui/`, `src/services/crisp/`, `src/services/meilisearch/`

- [ ] **Step 8.1: Copy UI primitives from cloubet/front**

```bash
cp -r /Users/raphaelcangucu/projects/cloubet/front/src/components/Button/ front/src/components/ui/Button/
cp -r /Users/raphaelcangucu/projects/cloubet/front/src/components/Inputs/ front/src/components/ui/Inputs/
cp -r /Users/raphaelcangucu/projects/cloubet/front/src/components/Table/ front/src/components/ui/Table/
cp -r /Users/raphaelcangucu/projects/cloubet/front/src/components/Modals/ front/src/components/ui/Modals/
```

Remove any GraphQL imports or betting-specific logic from copied components.

- [ ] **Step 8.2: Create CopyButton primitive**

```tsx
// front/src/components/ui/CopyButton.tsx
import { useState } from "react";
import { Copy, Check } from "lucide-react";
import { Button } from "@heroui/react";

export function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  async function handleCopy() {
    await navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }
  return (
    <Button isIconOnly variant="light" size="sm" onPress={handleCopy} aria-label="Copy">
      {copied ? <Check className="w-4 h-4 text-success" /> : <Copy className="w-4 h-4" />}
    </Button>
  );
}
```

- [ ] **Step 8.3: Copy and disable Crisp + Meilisearch**

```bash
cp -r /Users/raphaelcangucu/projects/cloubet/front/src/services/liveChat/ front/src/services/crisp/
cp -r /Users/raphaelcangucu/projects/cloubet/front/src/services/ front/src/services/meilisearch/ 2>/dev/null || true
```

In each service, wrap initialization with the feature flag:
```ts
// front/src/services/crisp/index.ts
export function initCrisp() {
  if (process.env.NEXT_PUBLIC_CRISP_ENABLED !== "true") return;
  // ... Crisp initialization
}
```

- [ ] **Step 8.4: Build to verify**
```bash
cd front && npm run build 2>&1 | tail -10
```
Expected: build succeeds.

- [ ] **Step 8.5: Commit**
```bash
git add front/src/components/ui/ front/src/services/ front/src/components/ui/CopyButton.tsx
git commit -m "feat: copy UI primitives, add CopyButton, disable Crisp and Meilisearch"
```

---

## Task 9: Final Verification

- [ ] **Step 9.1: Run full unit test suite**
```bash
cd front && npm run test 2>&1 | tail -10
```
Expected: all PASS.

- [ ] **Step 9.2: Run full E2E suite**
```bash
cd front && npx playwright test --reporter=line
```
Expected: all PASS.

- [ ] **Step 9.3: Build for Cloudflare**
```bash
cd front && npm run build
```
Expected: build succeeds with no type errors.

- [ ] **Step 9.4: Tag completion commit**
```bash
git commit --allow-empty -m "feat: frontend foundation complete (design system, shell, auth pages, landing)"
```
