// Polyfill ResizeObserver for jsdom (used by antd Input.TextArea, Table, etc.)
if (typeof ResizeObserver === 'undefined') {
  (globalThis as typeof globalThis & { ResizeObserver: unknown }).ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => true,
  }),
});
