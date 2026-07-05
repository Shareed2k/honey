import type { ReactNode } from 'react';
import { NavigationProvider } from './NavigationContext';
import { AppProvider } from './AppContext';
import { HostSelectionProvider } from './HostSelectionContext';
import { TerminalProvider } from './TerminalContext';
import { TunnelProvider } from './TunnelContext';
import { ReplayProvider } from './ReplayContext';
import { RecipeAssistProvider } from './RecipeAssistContext';

export function RootProvider({ children }: { children: ReactNode }) {
  return (
    <NavigationProvider>
      <AppProvider>
        <HostSelectionProvider>
          <TerminalProvider>
            <TunnelProvider>
              <ReplayProvider>
                <RecipeAssistProvider>
                  {children}
                </RecipeAssistProvider>
              </ReplayProvider>
            </TunnelProvider>
          </TerminalProvider>
        </HostSelectionProvider>
      </AppProvider>
    </NavigationProvider>
  );
}

export * from './NavigationContext';
export * from './AppContext';
export * from './HostSelectionContext';
export * from './TerminalContext';
export * from './TunnelContext';
export * from './ReplayContext';
export * from './RecipeAssistContext';
