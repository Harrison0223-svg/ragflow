import { z } from 'zod';
import { WebSearchProvider } from '@/constants/chat';

// Isolate unrelated assistant settings while exercising the real save schema.
jest.mock('@/components/llm-setting-items/next', () => ({
  LlmSettingFieldSchema: {},
  LlmSettingEnabledSchema: {},
}));
jest.mock('@/components/metadata-filter', () => ({ MetadataFilterSchema: {} }));
jest.mock('@/components/rerank-candidates-count-item', () => ({
  rerankCandidatesCountSchema: {},
}));
jest.mock('@/components/rerank', () => ({ rerankFormSchema: {} }));
jest.mock('@/components/similarity-slider', () => ({
  similarityThresholdSchema: {},
  keywordsSimilarityWeightSchema: {},
}));
jest.mock('@/components/top-n-item', () => ({ topnSchema: {} }));
jest.mock('@/hooks/common-hooks', () => ({
  useTranslate: () => ({ t: (key: string) => key }),
}));
jest.mock('./validate-chat-prompt', () => ({ chatPromptKbIssues: () => [] }));

// Require after mocks so the test also works with TypeScript-only transforms.
const { useChatSettingSchema } = require('./use-chat-setting-schema');

const AnySearchAssistant = {
  name: 'AnySearch test',
  icon: '',
  dataset_ids: [],
  llm_setting: {},
  prompt_config: {
    quote: true,
    keyword: false,
    tts: false,
    system: 'Test',
    refine_multiturn: false,
    web_search_provider: WebSearchProvider.AnySearch,
    anysearch_api_key: 'anysearch-test',
  },
};

describe('AnySearch assistant settings schema', () => {
  const schema: z.ZodTypeAny = useChatSettingSchema();

  it.each([undefined, '', '  '])(
    'rejects a missing or blank key: %s',
    (key) => {
      const result = schema.safeParse({
        ...AnySearchAssistant,
        prompt_config: {
          ...AnySearchAssistant.prompt_config,
          anysearch_api_key: key,
          tavily_api_key: 'other-test',
        },
      });
      expect(result.success).toBe(false);
      if (!result.success) {
        expect(
          result.error.issues.some(
            (issue) =>
              issue.path.join('.') === 'prompt_config.anysearch_api_key',
          ),
        ).toBe(true);
      }
    },
  );

  it('preserves vertical parameters and opt-in extraction on save', () => {
    const result = schema.parse({
      ...AnySearchAssistant,
      prompt_config: {
        ...AnySearchAssistant.prompt_config,
        anysearch_tag: ' code.doc ',
        anysearch_params: { library: 'golang' },
        anysearch_extract: true,
      },
    });
    expect(result.prompt_config.anysearch_tag).toBe('code.doc');
    expect(result.prompt_config.anysearch_params).toEqual({
      library: 'golang',
    });
    expect(result.prompt_config.anysearch_extract).toBe(true);
  });

  it('allows general search without advanced fields', () => {
    const result = schema.parse(AnySearchAssistant);
    expect(result.prompt_config.anysearch_extract).toBeUndefined();
    expect(result.prompt_config.anysearch_tag).toBeUndefined();
  });

  it.each([
    { anysearch_tag: 'code' },
    { anysearch_params: '{}' },
    { anysearch_extract: 'true' },
  ])('rejects invalid advanced fields: %j', (options) => {
    expect(
      schema.safeParse({
        ...AnySearchAssistant,
        prompt_config: { ...AnySearchAssistant.prompt_config, ...options },
      }).success,
    ).toBe(false);
  });
});
