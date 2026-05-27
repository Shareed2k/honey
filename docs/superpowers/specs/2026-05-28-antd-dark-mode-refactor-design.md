# Ant Design Dark Mode Refactor

**Date:** 2026-05-28  
**Branch:** add_ad_exec

## Context

The hostctl web UI is built with React 18 + TypeScript + Vite and has no component library — all styling is vanilla CSS. The main `App.tsx` is 2,543 lines handling all 8 tabs in a single file. The goal is to migrate to Ant Design v5 with dark mode, replacing the custom CSS shell and common controls while leaving specialized components (CodeMirror, xterm, xyflow, Swagger UI) intact. The migration also splits `App.tsx` into per-tab components since the new sidebar layout requires it and the file is already too large to maintain.

## Scope

**In scope (Approach B — shell + common controls):**
- App shell: navigation, layout, sidebar
- Common UI primitives: buttons, inputs, selects, modals, tables, cards, badges, tags, progress, alerts, tooltips, form elements
- Splitting `App.tsx` into per-tab components

**Out of scope:**
- CodeMirror editors (YAML/SQL) — no Ant Design equivalent; keep as-is
- xterm.js terminals and NoVNC viewer — keep as-is, wrap in antd `Modal`
- `@xyflow/react` diagram in RecipesTab — keep as-is, wrap in antd `Card`
- Swagger UI — keep as-is, wrap in antd `Card`
- Business logic, API layer, state management — no changes

## Architecture

### New File Structure

```
src/
├── App.tsx                    # ~100 lines: ConfigProvider + Layout + Sider + Menu
├── app.css                    # ~100 lines: overrides for xterm/CodeMirror/xyflow only
├── api.ts                     # Unchanged
├── hostReconcile.ts           # Unchanged
├── tabs/
│   ├── SearchTab.tsx          # Extracted from App.tsx (may accept a mode prop
│   │                          # reused by the Files tab — verify during extraction)
│   ├── BackendsTab.tsx        # Extracted from App.tsx
│   ├── ConfigTab.tsx          # Extracted from App.tsx
│   ├── TunnelsTab.tsx         # Extracted from App.tsx
│   └── ApiDocsTab.tsx         # Extracted from App.tsx
├── AppsTab/                   # Already a folder — stays, internal components migrated
└── RecipesTab/                # Already a folder — stays, internal components migrated
```

### App.tsx Shell Pattern

```tsx
<ConfigProvider theme={{ algorithm: theme.darkAlgorithm, token: { ... } }}>
  <Layout style={{ minHeight: '100vh' }}>
    <Sider collapsible width={180} theme="dark">
      <div className="logo">hostctl</div>
      <Menu theme="dark" mode="inline" selectedKeys={[activeTab]}
            items={menuItems} onSelect={({ key }) => setActiveTab(key)} />
    </Sider>
    <Layout.Content>
      {activeTab === 'search' && <SearchTab ... />}
      {/* one entry per tab */}
    </Layout.Content>
  </Layout>
</ConfigProvider>
```

URL state (`?tab=search`) and sessionStorage token auth are preserved unchanged.

## Packages

```
antd                  # v5 — component library with built-in dark algorithm
@ant-design/icons     # icon set for sidebar menu items
```

## Dark Theme Configuration

In `main.tsx`:

```tsx
import { ConfigProvider, theme } from 'antd'

<ConfigProvider theme={{
  algorithm: theme.darkAlgorithm,
  token: {
    colorBgBase: '#0f1115',
    colorPrimary: '#3d6fb8',
    borderRadius: 4,
  }
}}>
```

## Navigation — Sidebar

- Ant Design `Layout` + `Sider` (collapsible, 180px expanded / 48px icon-only collapsed)
- `Menu` in `inline` mode with one item per tab, each with an `@ant-design/icons` icon + label
- Active tab driven by `selectedKeys` state, synced to URL `?tab=` param as today

## Component Replacement Map

| Current | Ant Design |
|---|---|
| `<button>` + CSS | `Button` |
| `<input>`, `<textarea>` | `Input`, `Input.Search`, `Input.TextArea` |
| `<select>` | `Select` |
| Custom modal divs | `Modal` |
| Inline spinner CSS | `Spin` |
| Status text (online/offline) | `Tag` with color |
| Progress bars | `Progress` |
| Host list table | `Table` |
| Tunnel list | `Table` |
| Backend cards | `Card` |
| Flash messages | `message.success/error` |
| Inline alerts | `Alert` |
| Hover tooltips | `Tooltip` |
| Recipe form elements | `Form` + `Form.Item` |
| Section dividers | `Divider` |

## Specialized Components (unchanged)

These are placed in antd `Card` or `Modal` wrappers for consistent framing but their internals are not changed:

- **CodeMirror** — already uses `@codemirror/theme-one-dark`
- **xterm.js + NoVNC** — wrapped in antd `Modal` (same behavior as today)
- **@xyflow/react** — override CSS vars to match dark background; wrapped in `Card`
- **Swagger UI** — wrapped in `Card`

`app.css` retains only the overrides needed for these (~100 lines).

## Verification

1. `npm run dev` — app loads with sidebar navigation, dark theme applied globally
2. Click each tab — correct component renders, URL param updates
3. Sidebar collapse/expand — works, content area reflows
4. Open a terminal session — xterm modal opens, functions correctly
5. Open RecipesTab — xyflow diagram renders, step wizard works
6. Open ConfigTab — CodeMirror YAML editor renders and validates
7. Open AppsTab — SQL editor and proxy sessions work
8. `npm run build` — no TypeScript errors, build succeeds
9. `npm test` — existing tests pass (no business logic changed)
