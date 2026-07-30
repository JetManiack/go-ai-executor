function parseHash() {
  let hash = window.location.hash;
  if (!hash || hash === "#") {
    hash = "#/sandboxes";
  }
  const withoutHash = hash.slice(1);
  const [pathPart, queryPart] = withoutHash.split("?");
  const path = pathPart || "/sandboxes";
  const query = Object.fromEntries(new URLSearchParams(queryPart || ""));
  return { path, query };
}

export function navigate(path, query) {
  const hasQuery = query && Object.keys(query).length > 0;
  const qs = hasQuery ? "?" + new URLSearchParams(query).toString() : "";
  window.location.hash = "#" + path + qs;
}

export function useHashRoute() {
  const [route, setRoute] = React.useState(parseHash());

  React.useEffect(() => {
    function onHashChange() {
      setRoute(parseHash());
    }
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  return route;
}
