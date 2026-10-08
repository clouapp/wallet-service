# Wallet Popover + Currency Display UI — Implementation Plan

> **For agentic workers:** Implement task-by-task using checkbox (`- [ ]`) steps. Prefer **(A)** a fresh subagent per task with review between tasks.

**Goal:** Redesign the header WalletSelector into a previsions/cloubet-style WalletPopover with reusable `MoneyDisplay` and `CurrencyIcon` components, crypto/fiat balance toggle, search, hide-zero toggle, and fiat preference flow.

**Architecture:** Port the previsions/front pattern — `MoneyDisplay` (single-line, shows either crypto or fiat based on `displayInfiat` prop), `CurrencyIcon` (resolves code to icon with fallback), `SelectWallet` popover (search + scrollable list + footer toggles). Adapt from Vue/Redux to our React/SWR hooks (`useCurrencies`, `usePreferences`).

**Tech Stack:** React 19, TypeScript, HeroUI (`Popover`), TailwindCSS, SWR, lucide-react icons.

**Reference codebase:** `/home/raphaelcangucu/previsions/front` (cloubet)

---

## File Map

### Create

| File | Responsibility |
|------|---------------|
| `src/components/MoneyDisplay/index.tsx` | Reusable single-line currency display (crypto or fiat) |
| `src/components/CurrencyIcon/index.tsx` | Reusable currency icon with fallback |
| `src/components/Wallets/WalletPopover.tsx` | Header wallet popover (trigger + panel) |
| `src/components/Wallets/WalletPopoverList.tsx` | Popover body: search, wallet list, footer toggles |

### Modify

| File | Change |
|------|--------|
| `src/components/Header/Header.tsx` | Replace inline `WalletSelector` with `<WalletPopover />` |
| `src/config/chainIcons.ts` | Add `getCurrencyIconUrl()` merging all icon maps |
| `src/components/Assets/AssetsByWallets.tsx` | Use `CurrencyIcon` + `MoneyDisplay` |
| `src/components/Assets/AssetsByAssets.tsx` | Use `CurrencyIcon` + `MoneyDisplay` |
| `src/components/Settings/CurrencyPreferenceTab.tsx` | Add `CurrencyIcon` next to fiat radio options |

---

## Task 1: CurrencyIcon component

**Ported from:** `previsions/front/src/components/CurrencyIcon/index.tsx`

**Files:**
- Create: `src/components/CurrencyIcon/index.tsx`
- Modify: `src/config/chainIcons.ts`

- [ ] **Step 1: Extend chainIcons.ts**

Add a unified `getCurrencyIconUrl(code)` that checks `CHAIN_ICON_URLS`, then `TOKEN_ICON_URLS`, then returns a CoinMarketCap fallback URL:

```typescript
export function getCurrencyIconUrl(code: string): string {
  const lower = code.toLowerCase();
  return CHAIN_ICON_URLS[lower] ?? TOKEN_ICON_URLS[lower] ?? "";
}
```

- [ ] **Step 2: Create CurrencyIcon component**

```typescript
// src/components/CurrencyIcon/index.tsx
import { useState } from "react";
import { getCurrencyIconUrl } from "@/config/chainIcons";

interface CurrencyIconProps {
  code: string;
  logo?: string;
  size?: number;
  className?: string;
}

export function CurrencyIcon({ code, logo, size = 20, className = "" }: CurrencyIconProps) {
  const [hasError, setHasError] = useState(false);
  const src = logo || getCurrencyIconUrl(code);

  if (!src || hasError) {
    return (
      <span
        className={`inline-flex items-center justify-center rounded-full bg-primary/10 text-primary text-xs font-bold ${className}`}
        style={{ width: size, height: size }}
      >
        {code.slice(0, 2).toUpperCase()}
      </span>
    );
  }

  return (
    <img
      src={src}
      alt={`${code} icon`}
      width={size}
      height={size}
      className={`rounded-full ${className}`}
      onError={() => setHasError(true)}
      loading="lazy"
    />
  );
}
```

- [ ] **Step 3: Verify build**

---

## Task 2: MoneyDisplay component

**Ported from:** `previsions/front/src/components/MoneyDisplay/index.tsx`

Simplified for macro-wallets: uses our `useCurrencies` hook instead of Redux. Shows either crypto amount or fiat equivalent based on `displayInfiat` prop.

**Files:**
- Create: `src/components/MoneyDisplay/index.tsx`

- [ ] **Step 1: Create MoneyDisplay**

```typescript
// src/components/MoneyDisplay/index.tsx
import { useCurrencies } from "@/hooks/useCurrencies";

interface MoneyDisplayProps {
  amount: number;
  code: string;
  displayInFiat?: boolean;
  fiatCode?: string;
  showCryptoSymbol?: boolean;
  showFiatSymbol?: boolean;
  className?: string;
}

export function MoneyDisplay({
  amount,
  code,
  displayInFiat = false,
  fiatCode,
  showCryptoSymbol = false,
  showFiatSymbol = true,
  className = "",
}: MoneyDisplayProps) {
  const { convertCryptoToFiat, formatFiat, cryptos } = useCurrencies();
  // Resolve fiat code from hook default if not passed
  // We import usePreferences lazily to avoid circular deps — caller should pass fiatCode

  const crypto = cryptos.find((c) => c.code.toUpperCase() === code.toUpperCase());
  const decimals = crypto?.subunits ?? 8;

  if (!displayInFiat) {
    const formatted = Number(amount).toFixed(decimals);
    return (
      <span className={`whitespace-nowrap ${className}`}>
        {formatted}{showCryptoSymbol ? ` ${code}` : ""}
      </span>
    );
  }

  const targetFiat = fiatCode ?? "USD";
  const fiatValue = convertCryptoToFiat(amount, code.toUpperCase(), targetFiat);
  const display = formatFiat(fiatValue, targetFiat);

  return (
    <span className={`whitespace-nowrap ${className}`}>
      {showFiatSymbol ? display : fiatValue.toFixed(2)}
    </span>
  );
}
```

- [ ] **Step 2: Verify build**

---

## Task 3: WalletPopover (header trigger + popover shell)

**Ported from:** `previsions/front/src/components/Inputs/SelectWallet/index.tsx`

Uses HeroUI `Popover` (same pattern as `ProfileButton.tsx`).

**Files:**
- Create: `src/components/Wallets/WalletPopover.tsx`
- Modify: `src/components/Header/Header.tsx`

- [ ] **Step 1: Create WalletPopover**

Trigger shows:
- When `displayInFiat`: fiat symbol + total fiat balance of all wallets
- When `!displayInFiat`: chain icon + native balance of selected wallet (integer white, fraction dimmed)
- Tooltip shows the "other" denomination (same as previsions `SelectWallet`)
- Chevron rotates on open

Panel renders `<WalletPopoverList />` (Task 4).

- [ ] **Step 2: Replace WalletSelector in Header.tsx**

Remove the inline `WalletSelector` function. Import and render `<WalletPopover />` in its place.

- [ ] **Step 3: Verify build**

---

## Task 4: WalletPopoverList (popover body)

**Ported from:** `previsions/front/src/components/Inputs/SelectWallet/index.tsx` (dropdown body section)

**Files:**
- Create: `src/components/Wallets/WalletPopoverList.tsx`

- [ ] **Step 1: Create WalletPopoverList**

Layout (top to bottom):
1. **Search input** + **settings gear button** (navigates to `/dashboard/settings?tab=currency`)
2. **Scrollable wallet list** (max-h 300px, HeroUI `ScrollShadow`):
   - Per row: `CurrencyIcon` + wallet label + `MoneyDisplay` (crypto or fiat based on toggle)
   - Click navigates to `/dashboard/wallets/{id}` and closes popover
3. **Footer** with dividers:
   - "View in fiat" — `Switch` that calls `updatePreferences({ display_in_fiat: value })`
   - When toggling ON for the first time → also navigate to `/dashboard/settings?tab=currency`
   - "Hide zero balances" — `Switch` backed by localStorage

- [ ] **Step 2: Verify build**

---

## Task 5: Wire CurrencyIcon + MoneyDisplay into asset pages

**Files:**
- Modify: `src/components/Assets/AssetsByWallets.tsx`
- Modify: `src/components/Assets/AssetsByAssets.tsx`

- [ ] **Step 1: Update AssetsByWallets**

In the wallet name column, add `<CurrencyIcon code={wallet.chain} size={20} />` before the wallet label.

Replace the inline balance formatting with `<MoneyDisplay amount={...} code={...} displayInFiat={displayInFiat} fiatCode={activeFiatCode} />` for the primary line, and show the "other" denomination as secondary text.

- [ ] **Step 2: Update AssetsByAssets**

In the asset column, add `<CurrencyIcon code={group.chain} size={24} />` before the chain name.

Use `<MoneyDisplay>` for balance display.

- [ ] **Step 3: Verify build**

---

## Task 6: Add CurrencyIcon to fiat preference settings

**Files:**
- Modify: `src/components/Settings/CurrencyPreferenceTab.tsx`

- [ ] **Step 1: Add icons to radio options**

For each fiat radio option, show `<CurrencyIcon code={fiat.code} logo={fiat.logo} size={16} />` next to the symbol and code. The `Currency` model has a `logo` field — for fiats without logos, the `CurrencyIcon` fallback renders a two-letter avatar.

- [ ] **Step 2: Verify build**

---

## Task 7: Typecheck and final verify

- [ ] Run `npx tsc --noEmit` — no new type errors
- [ ] Visual verify: popover matches previsions/cloubet layout flow
