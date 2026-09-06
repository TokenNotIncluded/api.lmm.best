/*
Copyright (C) 2026 LIghtJUNction
*/

if (typeof globalThis.matchMedia !== 'function') {
  Object.defineProperty(globalThis, 'matchMedia', {
    configurable: true,
    value: (media) => ({
      matches: false,
      media,
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent() {
        return false
      },
    }),
  })
}

if (typeof globalThis.customElements === 'undefined') {
  const registry = new Map()
  Object.defineProperty(globalThis, 'customElements', {
    configurable: true,
    value: {
      define(name, constructor) {
        registry.set(name, constructor)
      },
      get(name) {
        return registry.get(name)
      },
    },
  })
}
