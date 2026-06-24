import React, { createContext, useContext, useReducer, ReactNode } from 'react';
import type { HostRecord } from '../HostPicker';
import type { ParsedRecipe } from '../api/types/recipes';
import type { WizardState, WizardStep, RecipeRef, EnvPair } from './types';
import { INITIAL_WIZARD_STATE } from './types';

type WizardAction =
  | { type: 'SET_HOSTS'; payload: HostRecord[] }
  | { type: 'GO_STEP'; payload: WizardStep }
  | { type: 'SET_BASE_RECIPE'; payload: ParsedRecipe | null }
  | { type: 'SET_RECIPE_REF'; payload: RecipeRef | null; edits: ParsedRecipe | null }
  | { type: 'SET_EDITS'; payload: ParsedRecipe | null }
  | { type: 'SET_ENV_OVERRIDES'; payload: EnvPair[] }
  | { type: 'SET_SSH_USER'; payload: string }
  | { type: 'SET_RECORD_SESSION'; payload: boolean }
  | { type: 'RESET'; payload?: { hosts: HostRecord[] } };

type ExtendedWizardState = WizardState & {
  baseRecipe: ParsedRecipe | null;
};

const initialState: ExtendedWizardState = {
  ...INITIAL_WIZARD_STATE,
  baseRecipe: null,
};

function wizardReducer(state: ExtendedWizardState, action: WizardAction): ExtendedWizardState {
  switch (action.type) {
    case 'SET_HOSTS':
      return { ...state, hosts: action.payload };
    case 'GO_STEP':
      return { ...state, step: action.payload };
    case 'SET_BASE_RECIPE':
      return { ...state, baseRecipe: action.payload };
    case 'SET_RECIPE_REF':
      return { ...state, recipe: action.payload, edits: action.edits, step: 3 };
    case 'SET_EDITS':
      return { ...state, edits: action.payload };
    case 'SET_ENV_OVERRIDES':
      return { ...state, envOverrides: action.payload };
    case 'SET_SSH_USER':
      return { ...state, sshUser: action.payload };
    case 'SET_RECORD_SESSION':
      return { ...state, recordSession: action.payload };
    case 'RESET':
      return { ...initialState, hosts: action.payload?.hosts || state.hosts };
    default:
      return state;
  }
}

type WizardContextType = {
  state: ExtendedWizardState;
  dispatch: React.Dispatch<WizardAction>;
};

const WizardContext = createContext<WizardContextType | null>(null);

export function WizardProvider({ children, initialHosts = [] }: { children: ReactNode; initialHosts?: HostRecord[] }) {
  const [state, dispatch] = useReducer(wizardReducer, { ...initialState, hosts: initialHosts });

  return <WizardContext.Provider value={{ state, dispatch }}>{children}</WizardContext.Provider>;
}

// eslint-disable-next-line react-refresh/only-export-components
export function useWizard() {
  const ctx = useContext(WizardContext);
  if (!ctx) {
    throw new Error('useWizard must be used within a WizardProvider');
  }
  return ctx;
}
