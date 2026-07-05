import { ToolRegistry } from './ToolRegistry';
import { ListBackendsTool } from './tools/ListBackendsTool';
import { SearchHostsTool } from './tools/SearchHostsTool';
import { ValidateRecipeTool } from './tools/ValidateRecipeTool';

export const agentToolRegistry = new ToolRegistry();
agentToolRegistry.register(new ListBackendsTool());
agentToolRegistry.register(new SearchHostsTool());
agentToolRegistry.register(new ValidateRecipeTool());
