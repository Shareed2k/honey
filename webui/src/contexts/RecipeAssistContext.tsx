import { createContext, useContext, useState, useEffect, Suspense, lazy, type ReactNode } from 'react';
import { Modal, Typography, Input, Select, Button, Spin, Alert } from 'antd';
import { apiGet } from '../api/core';
import { recipeAssist } from '../api/assist';
import { useHostSelection } from './HostSelectionContext';
import { useAppContext } from './AppContext';

const AiMarkdown = lazy(async () => import('../AiMarkdown').then((m) => ({ default: m.AiMarkdown })));

interface RecipeAssistContextType {
  openRecipeAssist: (path: string, name: string) => void;
}

const RecipeAssistContext = createContext<RecipeAssistContextType | null>(null);

export function RecipeAssistProvider({ children }: { children: ReactNode }) {
  const { sshUser, selectedRecords } = useHostSelection();
  const { meta } = useAppContext();

  const [recipeAssistOpen, setRecipeAssistOpen] = useState<{ path: string; name: string } | null>(null);
  const [recipeAssistModels, setRecipeAssistModels] = useState<string[]>([]);
  const [recipeAssistModelsLoading, setRecipeAssistModelsLoading] = useState(false);
  const [recipeAssistModelsErr, setRecipeAssistModelsErr] = useState<string | null>(null);
  const [recipeAssistSelectedModel, setRecipeAssistSelectedModel] = useState('');
  const [recipeAssistPrompt, setRecipeAssistPrompt] = useState('');
  const [recipeAssistBusy, setRecipeAssistBusy] = useState(false);
  const [recipeAssistErr, setRecipeAssistErr] = useState<string | null>(null);
  const [recipeAssistReply, setRecipeAssistReply] = useState('');

  useEffect(() => {
    if (!recipeAssistOpen || !meta?.terminal_assist_available) {
      return undefined;
    }
    let cancelled = false;
    setRecipeAssistModelsLoading(true);
    setRecipeAssistModelsErr(null);
    void (async () => {
      try {
        const r = await apiGet('/api/v1/terminal-assist/models');
        const j = (await r.json().catch(() => ({}))) as { models?: string[]; error?: string };
        if (cancelled) {
          return;
        }
        if (!r.ok) {
          setRecipeAssistModels([]);
          setRecipeAssistSelectedModel('');
          setRecipeAssistModelsErr(j.error || r.statusText || 'Could not load models');
          return;
        }
        const list = Array.isArray(j.models) ? j.models : [];
        setRecipeAssistModels(list);
        setRecipeAssistSelectedModel(list[0] ?? '');
        if (list.length === 0) {
          setRecipeAssistModelsErr('No models returned by the provider.');
        }
      } catch (e) {
        if (!cancelled) {
          setRecipeAssistModels([]);
          setRecipeAssistSelectedModel('');
          setRecipeAssistModelsErr(e instanceof Error ? e.message : String(e));
        }
      } finally {
        if (!cancelled) {
          setRecipeAssistModelsLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [recipeAssistOpen, meta?.terminal_assist_available]);

  const openRecipeAssist = (path: string, name: string) => {
    setRecipeAssistReply('');
    setRecipeAssistErr(null);
    setRecipeAssistPrompt('');
    setRecipeAssistOpen({ path, name });
  };

  const closeRecipeAssist = () => {
    setRecipeAssistOpen(null);
    setRecipeAssistReply('');
    setRecipeAssistErr(null);
    setRecipeAssistBusy(false);
  };

  const submitRecipeAssist = async () => {
    if (!recipeAssistOpen || !recipeAssistSelectedModel.trim()) {
      return;
    }
    setRecipeAssistBusy(true);
    setRecipeAssistErr(null);
    setRecipeAssistReply('');
    try {
      const { reply } = await recipeAssist({
        recipe_path: recipeAssistOpen.path,
        model: recipeAssistSelectedModel.trim(),
        user_prompt: recipeAssistPrompt.trim(),
        ssh_user: sshUser.trim(),
        records: selectedRecords,
      });
      setRecipeAssistReply(reply);
    } catch (e) {
      setRecipeAssistErr(e instanceof Error ? e.message : String(e));
    } finally {
      setRecipeAssistBusy(false);
    }
  };

  return (
    <RecipeAssistContext.Provider value={{ openRecipeAssist }}>
      {children}
      {recipeAssistOpen ? (
        <Modal maskClosable={false} open
          title={`AI explain: ${recipeAssistOpen.name}`}
          onCancel={() => closeRecipeAssist()}
          footer={<Button onClick={() => closeRecipeAssist()}>Close</Button>}
          width="min(640px, 96vw)"
          styles={{ body: { maxHeight: '80vh', overflow: 'auto', display: 'flex', flexDirection: 'column', gap: '0.55rem' } }}
        >
          <Typography.Text type="secondary" style={{ fontSize: '0.82rem' }}>
            Explanations are generated from the recipe file, optional dry-run against your{' '}
            <strong>selected hosts</strong> ({selectedRecords.length} selected), and your question. This is advisory—not
            a substitute for reviewing the CUE and dry-run output yourself before execute.
          </Typography.Text>
          {recipeAssistModelsLoading && <Spin size="small" />}
          {recipeAssistModelsErr && <Alert type="warning" message={recipeAssistModelsErr} />}
          {recipeAssistModels.length > 0 && (
            <div>
              <Typography.Text style={{ fontSize: '0.82rem' }}>Model</Typography.Text>
              <Select
                style={{ width: '100%', marginTop: 4 }}
                value={recipeAssistSelectedModel}
                onChange={setRecipeAssistSelectedModel}
                options={recipeAssistModels.map((id) => ({ value: id, label: id }))}
              />
            </div>
          )}
          <div>
            <Typography.Text style={{ fontSize: '0.82rem' }}>Question (optional)</Typography.Text>
            <Input.TextArea
              style={{ marginTop: 4 }}
              value={recipeAssistPrompt}
              onChange={(e) => setRecipeAssistPrompt(e.target.value)}
              placeholder="e.g. What does step 2 do on k8s pods?"
              rows={3}
            />
          </div>
          <Button
            type="primary"
            loading={recipeAssistBusy}
            disabled={recipeAssistModelsLoading || recipeAssistModels.length === 0 || !recipeAssistSelectedModel.trim()}
            onClick={() => void submitRecipeAssist()}
          >
            {recipeAssistBusy ? 'Thinking…' : 'Get explanation'}
          </Button>
          {recipeAssistErr && <Alert type="error" message={recipeAssistErr} />}
          {recipeAssistReply && (
            <div
              className="recipe-assist-reply"
              style={{ padding: '0.55rem', background: '#0f1115', border: '1px solid #2a3140', borderRadius: 6, maxHeight: '42vh', overflow: 'auto' }}
            >
              <Suspense fallback={<pre className="ai-markdown-suspense-fallback">{recipeAssistReply}</pre>}>
                <AiMarkdown content={recipeAssistReply} />
              </Suspense>
            </div>
          )}
        </Modal>
      ) : null}
    </RecipeAssistContext.Provider>
  );
}

export function useRecipeAssist() {
  const ctx = useContext(RecipeAssistContext);
  if (!ctx) throw new Error('useRecipeAssist must be used within RecipeAssistProvider');
  return ctx;
}
