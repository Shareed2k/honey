import { createContext, useContext, useState, useEffect, type ReactNode } from 'react';

export type Tab = 'search' | 'files' | 'backends' | 'config' | 'recipes' | 'tunnels' | 'apps' | 'logs' | 'api-docs' | 'feedback' | 'agent' | 'studio' | 'devices';

interface NavigationContextType {
  tab: Tab;
  setTab: (tab: Tab) => void;
}

const NavigationContext = createContext<NavigationContextType | null>(null);

export function NavigationProvider({ children }: { children: ReactNode }) {
  const [tab, setTab] = useState<Tab>(() => {
    const params = new URLSearchParams(window.location.search);
    const val = params.get('tab');
    if (
      val === 'search' ||
      val === 'files' ||
      val === 'backends' ||
      val === 'config' ||
      val === 'recipes' ||
      val === 'tunnels' ||
      val === 'apps' ||
      val === 'logs' ||
      val === 'api-docs' ||
      val === 'feedback' ||
      val === 'studio'
    ) {
      return val as Tab;
    }
    return 'search';
  });

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const originalString = params.toString();

    if (tab && tab !== 'search') params.set('tab', tab);
    else params.delete('tab');

    if (params.toString() !== originalString) {
      window.history.replaceState(null, '', `?${params.toString()}`);
    }
  }, [tab]);

  return (
    <NavigationContext.Provider value={{ tab, setTab }}>
      {children}
    </NavigationContext.Provider>
  );
}

export function useNavigation() {
  const ctx = useContext(NavigationContext);
  if (!ctx) throw new Error('useNavigation must be used within NavigationProvider');
  return ctx;
}
