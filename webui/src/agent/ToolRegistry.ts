import { Tool } from '@ag-ui/client';

export interface ToolContext {
  // Add contextual properties as needed
  sshUser?: string;
  selectedRecords?: unknown[];
}

export interface ToolExecutor {
  definition: Tool;
  execute(args: Record<string, unknown>, context: ToolContext): Promise<string>;
}

export class ToolRegistry {
  private executors = new Map<string, ToolExecutor>();

  register(executor: ToolExecutor) {
    this.executors.set(executor.definition.name, executor);
  }

  getTools(): Tool[] {
    return Array.from(this.executors.values()).map(e => e.definition);
  }

  async execute(toolName: string, args: Record<string, unknown>, context: ToolContext = {}): Promise<string> {
    const executor = this.executors.get(toolName);
    if (!executor) {
      return `Unknown tool: ${toolName}`;
    }
    return executor.execute(args, context);
  }
}
