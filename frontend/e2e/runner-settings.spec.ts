import { test, expect } from '@playwright/test';

test.describe('CLI Agent Runner Detection, Settings & Task Creation', () => {
  test('detects CLI agents, allows selecting agy in settings, and creates a throwaway task with agy runner', async ({ page }) => {
    // Inject mock Wails runtime before any script executes
    await page.addInitScript(() => {
      const state = {
        settings: {
          default_runner: 'claude',
          key_prefix: 'CV',
        } as Record<string, string>,
        savedSettings: [] as [string, string][],
        createdTasks: [] as any[],
        tasks: [] as any[],
        taskRunnerUpdates: [] as [string, string][],
      };

      (window as any).__mockState = state;

      (window as any).runtime = {
        EventsOnMultiple: () => () => {},
        EventsOn: () => () => {},
        EventsOff: () => {},
        EventsOffAll: () => {},
        LogPrint: () => {},
        LogTrace: () => {},
        LogDebug: () => {},
        LogInfo: () => {},
        LogWarning: () => {},
        LogError: () => {},
      };

      const appMock: Record<string, any> = {
        DispatcherState: async () => 'idle',
        Learnings: async () => [],
        ListApps: async () => [],
        AppIcons: async () => ({}),
        ListTasks: async () => state.tasks,
        GetPromptDefaults: async () => ({ fresh: '', resume: '', protocol: 'test-protocol' }),
        ListRepos: async () => [
          { name: 'mock-repo', path: '/home/user/mock-repo' },
        ],
        ListAgents: async () => [],
        ListCLIAgents: async () => [
          {
            id: 'claude',
            name: 'Claude Code',
            command: 'claude',
            installed: true,
            path: '/usr/local/bin/claude',
          },
          {
            id: 'agy',
            name: 'Antigravity',
            command: 'agy',
            installed: true,
            path: '/home/surya/.gemini/antigravity-cli/bin/agy',
          },
        ],
        GetSettings: async () => state.settings,
        SetSetting: async (k: string, v: string) => {
          state.settings[k] = v;
          state.savedSettings.push([k, v]);
        },
        CreateTask: async (input: any) => {
          const taskKey = `${state.settings.key_prefix || 'CV'}-${state.tasks.length + 1}`;
          const taskObj = {
            id: state.tasks.length + 1,
            key: taskKey,
            title: input.Title || 'Untitled',
            status: input.Start ? 'todo' : 'backlog',
            runner: input.Runner || '',
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
            repos: (input.Repos || []).map((r: string) => ({ repo_name: r, repo_id: 1, is_primary: r === input.Primary })),
          };
          state.createdTasks.push(input);
          state.tasks.push(taskObj);
          return taskKey;
        },
        GetTask: async (k: string) => {
          const t = state.tasks.find((x: any) => x.key === k) || {
            id: 1,
            key: k,
            title: 'Throwaway task with agy runner',
            status: 'todo',
            runner: 'agy',
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
            repos: [{ repo_id: 1, repo_name: 'mock-repo', is_primary: true }],
          };
          return {
            task: t,
            comments: [],
            runs: [],
            relations: {
              parent: null,
              children: [],
              links: [],
              blocked_by_all: [],
              blocks_all: [],
              ancestors: [],
              descendants: [],
            },
          };
        },
        SetTaskRunner: async (k: string, runner: string) => {
          state.taskRunnerUpdates.push([k, runner]);
          const t = state.tasks.find((x: any) => x.key === k);
          if (t) t.runner = runner;
        },
        Actions: async () => [],
        Sessions: async () => [],
        GetTaskRuns: async () => [],
        ListComments: async () => [],
      };

      (window as any).go = {
        ui: {
          App: new Proxy(appMock, {
            get(target, prop: string) {
              if (prop in target) return target[prop];
              return async (...args: any[]) => {
                return [];
              };
            },
          }),
        },
      };
    });

    // 1. Visit the app
    await page.goto('/');

    // 2. Navigate to Settings page
    const settingsNav = page.locator('[aria-label="Settings"]');
    await expect(settingsNav).toBeVisible();
    await settingsNav.click();

    // Verify Settings title is displayed
    await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible();

    // 3. Check General tab is active and "Default CLI agent" dropdown is present
    const runnerSelect = page.getByRole('combobox', { name: 'Default CLI agent' });
    await expect(runnerSelect).toBeVisible();

    // Click the dropdown to view detected options
    await runnerSelect.click();

    // Dropdown should list detected Claude Code and Antigravity (agy)
    const agyOption = page.getByRole('option', { name: /Antigravity \(agy\)/ });
    await expect(agyOption).toBeVisible();
    await expect(agyOption).toContainText('detected');

    const claudeOption = page.getByRole('option', { name: /Claude Code \(claude\)/ });
    await expect(claudeOption).toBeVisible();
    await expect(claudeOption).toContainText('detected');

    // 4. Select Antigravity (agy) manually
    await agyOption.click();

    // Find and click the Save button next to Default CLI agent
    const runnerRow = page.locator('.mantine-Group-root').filter({ has: runnerSelect });
    const saveButton = runnerRow.getByRole('button', { name: 'Save' });
    await expect(saveButton).toBeEnabled();
    await saveButton.click();

    // Verify mock state has updated default_runner to agy
    const updatedSettings = await page.evaluate(() => (window as any).__mockState.settings);
    expect(updatedSettings.default_runner).toBe('agy');

    // 5. Open New Task modal
    await page.locator('button').filter({ hasText: 'New task' }).first().click();
    await expect(page.getByRole('dialog')).toBeVisible();

    // 6. Fill in throwaway task details
    await page.getByPlaceholder('Title').fill('Throwaway task with agy runner');

    // Select the mock repo
    const reposInput = page.getByPlaceholder('Pick repos');
    await reposInput.click();
    await page.getByRole('option', { name: 'mock-repo' }).click();

    // Check the Agent runner dropdown in the New Task modal
    const taskRunnerSelect = page.getByRole('combobox', { name: 'Agent runner' });
    await expect(taskRunnerSelect).toBeVisible();
    await taskRunnerSelect.click();

    // Verify options in New Task modal include Default runner, Claude Code, and Antigravity
    await expect(page.getByRole('option', { name: 'Default runner' })).toBeVisible();
    const taskAgyOption = page.getByRole('option', { name: /Antigravity \(agy\)/ });
    await expect(taskAgyOption).toBeVisible();

    // Select Antigravity explicitly
    await taskAgyOption.click();

    // Submit the form
    const createButton = page.getByRole('button', { name: 'Create', exact: true });
    await expect(createButton).toBeEnabled();
    await createButton.click();

    // Verify task creation was called with Runner = 'agy'
    const createdTasks = await page.evaluate(() => (window as any).__mockState.createdTasks);
    expect(createdTasks.length).toBe(1);
    expect(createdTasks[0].Title).toBe('Throwaway task with agy runner');
    expect(createdTasks[0].Runner).toBe('agy');
    expect(createdTasks[0].Repos).toEqual(['mock-repo']);

    // 7. Verify Task Workspace navigation and TaskSettings runner update
    await expect(page.getByText('Throwaway task with agy runner').first()).toBeVisible();

    // Open TaskSettings modal via "More settings" icon
    const moreSettingsBtn = page.locator('[aria-label="More settings"]');
    await expect(moreSettingsBtn).toBeVisible();
    await moreSettingsBtn.click();

    // Verify Task Settings modal opened with RunnerSelect
    const taskSettingsDialog = page.getByRole('dialog', { name: /settings/i });
    await expect(taskSettingsDialog).toBeVisible();
    const taskSettingsRunnerSelect = taskSettingsDialog.getByRole('combobox', { name: 'Agent runner' });
    await expect(taskSettingsRunnerSelect).toBeVisible();

    // Change runner to claude in task settings
    await taskSettingsRunnerSelect.click();
    await page.getByRole('option', { name: /Claude Code \(claude\)/ }).click();

    // Verify SetTaskRunner was called
    const runnerUpdates = await page.evaluate(() => (window as any).__mockState.taskRunnerUpdates);
    expect(runnerUpdates.length).toBe(1);
    expect(runnerUpdates[0][1]).toBe('claude');
  });
});
