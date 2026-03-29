# Layout Shell Overhaul — Collapsible Sidebar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the static `w-56` sidebar in DashboardLayout with a collapsible open/closed sidebar that exactly matches the cloubet/front (`/Users/raphaelcangucu/projects/cloubet/front`) design pattern.

**Architecture:** `DashboardLayout` manages an `isMenuOpen` boolean state (persisted to `localStorage` under key `"vault_menu_open"`). It passes `isOpen` + `toggle` down to `WebMenu`. `WebMenu` renders itself with `menuOpen` (min-w-56) or `menuClose` (w-[60px]) CSS classes and hosts the toggle button. The sidebar is `fixed` positioned (taken out of flow). `DashboardLayout` compensates by applying a dynamic `pl-[60px]` or `pl-56` to the `bodyWrapper` div so content is not hidden behind the fixed sidebar.

**Tech Stack:** Next.js 15 (Pages Router), React, Tailwind CSS, HeroUI, Lucide icons, Redux Toolkit, Vitest (tests must live under `front/tests/`), Playwright E2E.

**Design reference:** `/Users/raphaelcangucu/projects/cloubet/front/src/components/Menu/styles.ts` and `src/services/providers/LayoutProvider.tsx` (branch: `dev`).

> **Note on test location:** Vitest is configured with `include: ["tests/**/*.test.{ts,tsx}"]` — all new test files go under `front/tests/`, NOT co-located with source files.

---

## File Map

| Status | File | Change |
|--------|------|--------|
| Modify | `front/src/layouts/DashboardLayout.tsx` | Add `isMenuOpen` state + `localStorage` persistence; replace static `<aside>` with `menuWrapper` div + dynamic `paddingLeft` on body |
| Verify/no-op | `front/src/styles/global.ts` | Verify `menuWrapper`, `bodyWrapper`, `mainWrapper`, `pagesWrapper` already present (they are) |
| Modify | `front/src/components/Menu/WebMenu.tsx` | Add `isOpen` + `onToggle` props; render `menuOpen`/`menuClose` nav; add toggle `<MenuIcon>` button in header; icon-only links when closed |
| Verify/no-op | `front/src/components/Menu/styles.ts` | Verify `menuHeader`, `ul`, `menuItems`, `menuOpen`, `menuClose` all match cloubet reference |
| Modify | `front/src/components/Header/Header.tsx` | Ensure hamburger button has `md:hidden` class |
| New | `front/tests/components/Menu/WebMenu.test.tsx` | Vitest: renders open state, renders closed state, toggle button fires callback |
| New | `front/tests/layouts/DashboardLayout.test.tsx` | Vitest: renders children, sidebar nav present |

---

## Task 1: Verify `Menu/styles.ts` and `styles/global.ts` are correct

**Files:**
- Read: `front/src/components/Menu/styles.ts`
- Read: `front/src/styles/global.ts`

- [ ] **Step 1: Verify Menu/styles.ts exports**

  Read `front/src/components/Menu/styles.ts`. Confirm it exports:
  - `menuReponsive` — contains `sm:fixed xm:fixed md:relative lg:relative`
  - `menuOpen` — contains `min-w-56`
  - `menuClose` — contains `w-[60px]`
  - `menuHeader` (or named `header`) — contains `h-16 flex gap-2 items-center`
  - `ul` — contains `flex flex-col pt-2 pb-8 gap-3 flex-1 overflow-auto`
  - `menuItems` — contains `bg-accent dark:bg-accent-dark`
  - `menuItemLink` — contains hover styles
  - `menuItemLinkActive` — contains `border-l-2 border-primary`

  Read `front/src/styles/global.ts`. Confirm it already exports:
  - `menuWrapper` — contains `z-50 h-full`
  - `bodyWrapper` — contains `flex flex-col flex-1 min-w-0`
  - `mainWrapper` — defined
  - `pagesWrapper` — contains `max-w-[1360px]`

  If any export is missing, add it now matching the cloubet reference exactly.

- [ ] **Step 2: Commit if any changes were made** (skip if nothing changed)

  ```bash
  cd /Users/raphaelcangucu/projects/bitgoclone
  git add front/src/components/Menu/styles.ts front/src/styles/global.ts
  git commit -m "style: align Menu/styles.ts and global.ts with cloubet/front tokens"
  ```

---

## Task 2: Refactor `WebMenu` to collapsible pattern

**Files:**
- Modify: `front/src/components/Menu/WebMenu.tsx`
- New: `front/tests/components/Menu/WebMenu.test.tsx`

The current WebMenu is always-open with no toggle. We need to add `isOpen`/`onToggle` props and render either full-label links (open) or icon-only links (closed).

- [ ] **Step 1: Write failing test**

  Create `front/tests/components/Menu/WebMenu.test.tsx`:

  ```typescript
  import { render, screen, fireEvent } from "@testing-library/react";
  import { Provider } from "react-redux";
  import { store } from "@/lib/store";
  import WebMenu from "@/components/Menu/WebMenu";

  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <Provider store={store}>{children}</Provider>
  );

  test("renders nav links when open", () => {
    render(<WebMenu isOpen={true} onToggle={() => {}} />, { wrapper });
    // When open, nav items render as full <Link> elements in the DOM
    const nav = document.querySelector("nav");
    expect(nav).toBeInTheDocument();
  });

  test("renders nav in closed state without error", () => {
    render(<WebMenu isOpen={false} onToggle={() => {}} />, { wrapper });
    const nav = document.querySelector("nav");
    expect(nav).toBeInTheDocument();
  });

  test("calls onToggle when toggle button clicked", () => {
    const onToggle = vi.fn();
    render(<WebMenu isOpen={true} onToggle={onToggle} />, { wrapper });
    fireEvent.click(screen.getByTestId("menu-toggle"));
    expect(onToggle).toHaveBeenCalledTimes(1);
  });
  ```

  Run: `cd front && npx vitest run tests/components/Menu/WebMenu.test.tsx`
  Expected: FAIL — `isOpen` and `onToggle` props not accepted yet

- [ ] **Step 2: Refactor WebMenu**

  Replace `front/src/components/Menu/WebMenu.tsx` with:

  ```typescript
  import Link from "next/link";
  import { useRouter } from "next/router";
  import { LayoutDashboard, Settings, Users, MenuIcon } from "lucide-react";
  import { useTranslation } from "react-i18next";
  import { cn } from "@/utils/cn";
  import {
    menuOpen,
    menuClose,
    menuHeader,
    ul,
    menuItems,
    menuItemLink,
    menuItemLinkActive,
  } from "./styles";

  interface WebMenuProps {
    isOpen: boolean;
    onToggle: () => void;
  }

  const navItems = [
    { labelKey: "nav.assets" as const, href: "/dashboard/assets", icon: LayoutDashboard },
    { labelKey: "nav.accounts" as const, href: "/dashboard/accounts", icon: Users },
  ];
  const bottomNavItems = [
    { labelKey: "nav.accountSettings" as const, href: "/dashboard/settings", icon: Settings },
  ];

  export default function WebMenu({ isOpen, onToggle }: WebMenuProps) {
    const { t } = useTranslation("common");
    const router = useRouter();
    const isActive = (href: string) => router.pathname.startsWith(href);

    const renderLink = (item: typeof navItems[number], placement: "main" | "bottom") => {
      const Icon = item.icon;
      const active = isActive(item.href);
      if (!isOpen) {
        return (
          <Link
            key={item.href}
            href={item.href}
            title={t(item.labelKey)}
            className={cn(
              "flex items-center justify-center py-2 px-2 rounded-md transition-colors",
              active
                ? "text-primary dark:text-primary-dark border-l-2 border-primary dark:border-primary-dark bg-surface dark:bg-surface-dark"
                : "text-p-primary dark:text-p-primary-dark hover:bg-surface dark:hover:bg-surface-dark"
            )}
          >
            <Icon size={18} />
          </Link>
        );
      }
      return (
        <Link
          key={item.href}
          href={item.href}
          className={cn(menuItemLink, active && menuItemLinkActive)}
        >
          <Icon size={18} />
          {t(item.labelKey)}
        </Link>
      );
    };

    return (
      <nav className={cn(isOpen ? menuOpen : menuClose)}>
        {/* Toggle button header */}
        <div className={menuHeader}>
          <button
            type="button"
            data-testid="menu-toggle"
            onClick={onToggle}
            aria-label="Toggle menu"
            className="p-2 rounded-md text-p-hint dark:text-p-hint-dark hover:text-p-primary dark:hover:text-p-primary-dark hover:bg-accent dark:hover:bg-accent-dark transition-colors"
          >
            <MenuIcon size={18} />
          </button>
        </div>

        {/* Nav list */}
        <ul className={ul}>
          <div className="flex-1 flex flex-col gap-3">
            <div className={menuItems}>
              {navItems.map((item) => renderLink(item, "main"))}
            </div>
          </div>

          <div className={menuItems}>
            {bottomNavItems.map((item) => renderLink(item, "bottom"))}
          </div>
        </ul>
      </nav>
    );
  }
  ```

- [ ] **Step 3: Run test to verify passes**

  ```bash
  cd front && npx vitest run tests/components/Menu/WebMenu.test.tsx
  ```
  Expected: PASS (3 tests)

- [ ] **Step 4: Commit**

  ```bash
  git add front/src/components/Menu/WebMenu.tsx front/tests/components/Menu/WebMenu.test.tsx
  git commit -m "feat: refactor WebMenu to collapsible sidebar matching cloubet/front pattern"
  ```

---

## Task 3: Refactor `DashboardLayout` to manage sidebar state + fix body offset

**Files:**
- Modify: `front/src/layouts/DashboardLayout.tsx`
- New: `front/tests/layouts/DashboardLayout.test.tsx`

The layout shell needs to:
1. Add `isMenuOpen` state with `localStorage` persistence (key: `"vault_menu_open"`, default: `true`)
2. Replace static `<aside>` with `<div className={menuWrapper}><WebMenu .../></div>`
3. Add dynamic `paddingLeft` to `bodyWrapper` div: `pl-[60px]` when closed, `pl-56` when open — because the sidebar uses `fixed` positioning and is taken out of flow
4. Keep `MobileMenu` for mobile fullscreen drawer

- [ ] **Step 1: Write test**

  Create `front/tests/layouts/DashboardLayout.test.tsx`:

  ```typescript
  import { render, screen } from "@testing-library/react";
  import { Provider } from "react-redux";
  import { store } from "@/lib/store";
  import DashboardLayout from "@/layouts/DashboardLayout";

  vi.mock("next/router", () => ({
    useRouter: () => ({ push: vi.fn(), pathname: "/dashboard/assets" }),
  }));

  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <Provider store={store}>{children}</Provider>
  );

  test("renders children inside layout", () => {
    render(<DashboardLayout><span>page content</span></DashboardLayout>, { wrapper });
    expect(screen.getByText("page content")).toBeInTheDocument();
  });

  test("layout contains a sidebar nav", () => {
    const { container } = render(<DashboardLayout><span /></DashboardLayout>, { wrapper });
    expect(container.querySelector("nav")).toBeInTheDocument();
  });
  ```

  Run: `cd front && npx vitest run tests/layouts/DashboardLayout.test.tsx`
  Expected: baseline check (may pass or fail depending on current state)

- [ ] **Step 2: Rewrite DashboardLayout**

  Replace `front/src/layouts/DashboardLayout.tsx`:

  ```typescript
  import { useState, useEffect, type ReactNode } from "react";
  import { useRouter } from "next/router";
  import { useSelector } from "react-redux";
  import Header from "@/components/Header/Header";
  import WebMenu from "@/components/Menu/WebMenu";
  import MobileMenu from "@/components/Menu/MobileMenu";
  import type { RootState } from "@/lib/store";
  import { background, menuWrapper, bodyWrapper, mainWrapper, pagesWrapper } from "@/styles/global";
  import { cn } from "@/utils/cn";

  const MENU_STORAGE_KEY = "vault_menu_open";

  interface DashboardLayoutProps {
    children: ReactNode;
  }

  export default function DashboardLayout({ children }: DashboardLayoutProps) {
    const [isMenuOpen, setIsMenuOpen] = useState(true);
    const [mobileOpen, setMobileOpen] = useState(false);
    const router = useRouter();
    const { isAuthenticated } = useSelector((s: RootState) => s.auth);

    // Rehydrate persisted state after mount (avoids SSR mismatch)
    useEffect(() => {
      const stored = localStorage.getItem(MENU_STORAGE_KEY);
      if (stored !== null) setIsMenuOpen(stored === "true");
    }, []);

    const toggleMenu = () => {
      setIsMenuOpen((prev) => {
        const next = !prev;
        localStorage.setItem(MENU_STORAGE_KEY, String(next));
        return next;
      });
    };

    useEffect(() => {
      if (!isAuthenticated) void router.push("/login");
    }, [isAuthenticated, router]);

    if (!isAuthenticated) return null;

    return (
      <div className={cn(background, "overflow-hidden")}>
        {/* Fixed sidebar — taken out of flow; body needs matching padding-left */}
        <div className={menuWrapper}>
          <WebMenu isOpen={isMenuOpen} onToggle={toggleMenu} />
        </div>

        {/* Mobile fullscreen drawer */}
        <MobileMenu open={mobileOpen} onClose={() => setMobileOpen(false)} />

        {/* Main content area — offset left to clear the fixed sidebar */}
        <div
          className={cn(
            bodyWrapper,
            isMenuOpen ? "pl-56" : "pl-[60px]",
            "transition-[padding-left] duration-100 ease-in-out"
          )}
        >
          <Header onMenuToggle={() => setMobileOpen(true)} />
          <main className={mainWrapper}>
            <div className={pagesWrapper}>{children}</div>
          </main>
        </div>
      </div>
    );
  }
  ```

- [ ] **Step 3: Run test to verify passes**

  ```bash
  cd front && npx vitest run tests/layouts/DashboardLayout.test.tsx
  ```
  Expected: PASS (2 tests)

- [ ] **Step 4: Commit**

  ```bash
  git add front/src/layouts/DashboardLayout.tsx front/tests/layouts/DashboardLayout.test.tsx
  git commit -m "feat: DashboardLayout uses collapsible fixed sidebar with dynamic body padding"
  ```

---

## Task 4: Ensure `Header` hamburger is mobile-only

**Files:**
- Read/Modify: `front/src/components/Header/Header.tsx`

- [ ] **Step 1: Read Header and verify the hamburger button class**

  Open `front/src/components/Header/Header.tsx`. Find the hamburger `<button>` that calls `onMenuToggle`. Confirm it has `md:hidden` in its className.

  If it has `md:hidden` already → no change needed. If it does NOT have `md:hidden` → add it so the button only shows on screens narrower than md.

  The button should look like:
  ```tsx
  <button
    type="button"
    className="md:hidden p-1.5 rounded-md text-p-hint dark:text-p-hint-dark ..."
    onClick={onMenuToggle}
    aria-label={t("header.toggleMenu")}
  >
    <Menu size={20} />
  </button>
  ```

- [ ] **Step 2: Commit if changed**

  ```bash
  git add front/src/components/Header/Header.tsx
  git commit -m "style: ensure Header hamburger is md:hidden (desktop toggle is in sidebar)"
  ```

---

## Task 5: Visual smoke test

- [ ] **Step 1: Start the dev server**

  ```bash
  cd /Users/raphaelcangucu/projects/bitgoclone/front && npm run dev
  ```

- [ ] **Step 2: Manually verify at http://localhost:3000/dashboard/assets**

  Check:
  - Sidebar renders in open state by default (labels visible, min-w-56 width)
  - Click the `<MenuIcon>` toggle inside the sidebar → collapses to icon-only (60px wide)
  - Main content shifts left correctly — no content hidden behind sidebar
  - Click toggle again → expands back to full width
  - Hard-refresh → sidebar state is preserved from localStorage
  - Resize to mobile → sidebar not visible (MobileMenu handles mobile)

---

## Task 6: Run all frontend tests

- [ ] **Step 1: Run full Vitest suite**

  ```bash
  cd front && npx vitest run
  ```
  Expected: All tests pass. Fix any regressions before continuing.

- [ ] **Step 2: Run the E2E auth suite**

  ```bash
  cd front && npx playwright test e2e/auth.spec.ts
  ```
  Expected: Auth scenarios pass with no layout regression.

- [ ] **Step 3: Final commit if tests pass**

  ```bash
  git add -A
  git commit -m "test: confirm layout overhaul — all tests green"
  ```

---

## Definition of Done

- [ ] Sidebar collapses to icon-only (w-[60px]) and expands to full labels (min-w-56) via toggle inside the nav
- [ ] State persists across page refreshes via `localStorage` key `vault_menu_open`
- [ ] Body content shifts left with `pl-56` / `pl-[60px]` matching sidebar width
- [ ] Visual result matches macro.markets / cloubet/front pattern
- [ ] All Vitest tests pass (`npx vitest run`)
- [ ] No Playwright E2E regressions on auth flow
