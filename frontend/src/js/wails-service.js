(function () {
  const root = (window.AlchementaryLink = window.AlchementaryLink || {});

  const directService = () => window.go?.api?.Service || null;
  const unavailable = (name) =>
    Promise.reject(new Error(`Wails service method is not available: ${String(name)}`));

  root.wailsService = new Proxy(
    {},
    {
      get(_target, prop) {
        if (prop === "then") return undefined;
        const service = directService();
        const value = service?.[prop];
        if (typeof value === "function") return value.bind(service);
        if (value !== undefined) return value;
        return () => unavailable(prop);
      },
      has(_target, prop) {
        return prop in (directService() || {});
      },
    },
  );

  root.wailsServiceReady = Promise.resolve(root.wailsService);
  root.hasWailsService = () =>
    Boolean(root.wailsService && window.runtime && directService());
})();
