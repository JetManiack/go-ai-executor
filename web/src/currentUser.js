export function useCurrentUser() {
  const [user, setUser] = React.useState(null);
  const [error, setError] = React.useState(null);

  React.useEffect(() => {
    fetch("/api/me")
      .then((res) => {
        if (!res.ok) {
          throw new Error("not authenticated");
        }
        return res.json();
      })
      .then(setUser)
      .catch((err) => setError(String(err)));
  }, []);

  return { user, error };
}
