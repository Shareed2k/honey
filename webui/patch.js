const fs = require('fs');
const file = 'src/api/types/recipes.ts';
let content = fs.readFileSync(file, 'utf8');

content = content.replace(
  "defaults?: Record<string, unknown>;",
  "defaults?: {\n    prompts?: Record<string, RecipePrompt>;\n    [key: string]: unknown;\n  };"
);

fs.writeFileSync(file, content);
