import {useState} from 'react'
import {ActionIcon, Button, Checkbox, Code, Group, Modal, MultiSelect, PasswordInput, ScrollArea, SegmentedControl, Select, SelectProps, Stack, Table, Tabs, Text, TextInput, Textarea, Title, Tooltip} from '@mantine/core'
import {IconAdjustments} from '@tabler/icons-react'
import {AddRepo, CreateTask, ExportState, GetPromptDefaults, GetTask, GetSettings, ImportState, ListAgents, ListCLIAgents, ListModels, ListRepos, ListTasks, PreviewBrief, SetRepoSetupScript, SetSetting, SetTaskAgent, SetTaskModel, SetTaskRunner} from '../wailsjs/go/ui/App'
import {core, store} from '../wailsjs/go/models'
import {Nav, errMsg} from './components'
import {Markdown, MarkdownEditor} from './markdown'
import {useData, useDraft} from './util'

export function NewTask({nav, close}: {nav: Nav; close: () => void}) {
    const {data: repos} = useData(ListRepos, [])
    const {data: tasks} = useData(ListTasks, [])
    const [parent, setParent] = useState('')
    const [title, setTitle] = useDraft('new-task:title')
    const [desc, setDesc] = useDraft('new-task:desc')
    const [sel, setSel] = useState<string[]>([])
    const [primary, setPrimary] = useState('')
    const [model, setModel] = useState('')
    const [agent, setAgent] = useState('')
    const [runner, setRunner] = useState('')
    const [start, setStart] = useState(false)
    const [epic, setEpic] = useState(false)
    const [epicName, setEpicName] = useState('')
    const prim = sel.includes(primary) ? primary : sel[0] ?? ''
    const submit = () =>
        CreateTask(core.NewTaskInput.createFrom({
            Title: title, Description: desc, Repos: sel, Primary: prim,
            Start: start, Model: model, Agent: agent, Runner: runner, ParentKey: parent, Epic: epic, EpicName: epicName.trim(),
        })).then(key => { setTitle(''); setDesc(''); close(); nav({name: 'task', key}) }, errMsg)
    // Picking a parent prefills repos, primary, agent and runner from it; they stay editable. Clearing leaves them.
    const pickParent = (key: string | null) => {
        setParent(key ?? '')
        if (key) GetTask(key).then(d => {
            setSel(d.task.repos?.map(r => r.repo_name) ?? [])
            setPrimary(d.task.repos?.find(r => r.is_primary)?.repo_name ?? '')
            setAgent(d.task.agent ?? '')
            setRunner(d.task.runner ?? '')
        }, errMsg)
    }
    return (
        <Modal opened onClose={close} title="New task" size="md">
            <Stack gap="sm">
                <TextInput data-autofocus placeholder="Title" value={title} onChange={e => setTitle(e.target.value)}/>
                <MarkdownEditor minRows={4} placeholder="Description" value={desc} onChange={setDesc} repos={sel}/>
                <Select label="Parent" placeholder="None" searchable clearable value={parent || null} onChange={pickParent}
                        data={(tasks ?? []).filter(t => t.status !== 'done' && t.status !== 'cancelled').map(t => ({value: t.key, label: `${t.key} ${t.title}`}))}/>
                <MultiSelect label="Repos" placeholder="Pick repos" description={repos?.length === 0 ? 'Add one on the Repos page first' : undefined}
                             data={repos?.map(r => r.name) ?? []} value={sel} onChange={setSel}/>
                {sel.length > 1 && <Select label="Primary repo" allowDeselect={false} data={sel} value={prim} onChange={v => setPrimary(v ?? '')}/>}
                <ModelSelect value={model} onChange={setModel}/>
                <AgentSelect repos={sel} value={agent} onChange={setAgent}/>
                <RunnerSelect value={runner} onChange={setRunner}/>
                <Checkbox label="Start now (todo)" checked={start} onChange={e => setStart(e.currentTarget.checked)}/>
                <Checkbox label="Epic" checked={epic} onChange={e => setEpic(e.currentTarget.checked)}/>
                {epic && <TextInput label="Epic name" placeholder="default: the lower-cased task key" value={epicName} onChange={e => setEpicName(e.target.value)}/>}
                <Group justify="flex-end">
                    <Button variant="default" onClick={() => { setTitle(''); setDesc(''); close() }}>Cancel</Button>
                    <Button onClick={submit} disabled={!title.trim() || sel.length === 0}>Create</Button>
                </Group>
            </Stack>
        </Modal>
    )
}

/** Select of the Claude agents available to the given repos; "" means none. */
export function AgentSelect({repos, value, onChange, size, label}: {repos: string[]; value: string; onChange: (a: string) => void; size?: string; label?: string}) {
    const {data} = useData(() => ListAgents(repos), [repos.join(',')])
    const names = [...new Set((data ?? []).map(a => a.name))]
    if (value && !names.includes(value)) names.push(value)
    return <Select size={size} label={label} placeholder="Agent: none" aria-label="Claude agent" clearable data={names} value={value || null} onChange={v => onChange(v ?? '')}/>
}

export function TaskAgent({k, repos, agent}: {k: string; repos: string[]; agent: string}) {
    return <AgentSelect size="xs" repos={repos} value={agent} onChange={a => SetTaskAgent(k, a).catch(errMsg)}/>
}

/** Select of CLI agent runners (claude, agy); "" means default. */
export function RunnerSelect({value, onChange, size, label}: {value: string; onChange: (r: string) => void; size?: string; label?: string}) {
    const {data: agents} = useData(ListCLIAgents, [])
    const runners = (agents ?? []).map(a => ({
        value: a.id,
        label: a.name + (a.installed ? '' : ' (not installed)')
    }))
    const options = [{value: '', label: 'Default runner'}, ...runners]
    return <Select size={size} label={label} placeholder="Runner: default" aria-label="Agent runner" clearable data={options} value={value || null} onChange={v => onChange(v ?? '')}/>
}

export function TaskRunner({k, runner}: {k: string; runner: string}) {
    return <RunnerSelect size="xs" value={runner} onChange={r => SetTaskRunner(k, r).catch(errMsg)}/>
}

export function RunnerSettingSelect({value, onChange, ...rest}: {value: string; onChange: (r: string) => void} & Omit<SelectProps, 'value' | 'onChange' | 'data'>) {
    const {data: agents} = useData(ListCLIAgents, [])
    const runners = (agents ?? []).map(a => ({
        value: a.id,
        label: a.name + (a.installed ? '' : ' (not installed)')
    }))
    return <Select aria-label="Default runner" allowDeselect={false} {...rest} value={value} onChange={v => onChange(v ?? '')} data={runners}/>
}

let modelsP: Promise<core.ModelList> | undefined
const listModels = () => modelsP ??= ListModels()

/** Model choice; "" means the default (task: the agent's or default_model setting). A value not in the list is kept as an option. */
export function ModelSelect({value, onChange, ...rest}: {value: string; onChange: (m: string) => void} & Omit<SelectProps, 'value' | 'onChange' | 'data'>) {
    const {data: ml} = useData(listModels, [])
    const ms = ml?.models ?? []
    const known = !value || ms.some(m => m.id === value || m.id.replace(/-\d{8}$/, '') === value || (m.latest && m.tier === value))
    const data = [{value: '', label: 'Default'},
        ...ms.filter(m => m.latest).map(m => ({value: m.tier, label: `${m.tier} (latest: ${m.id})`})),
        ...ms.map(m => ({value: m.id, label: m.id})),
        ...(known ? [] : [{value, label: value}])]
    return <Select aria-label="Model" allowDeselect={false} searchable {...rest} value={value} onChange={v => onChange(v ?? '')} data={data}/>
}

export function TaskSettings({k, model, runner, reload}: {k: string; model: string; runner?: string; reload: () => void}) {
    const [open, setOpen] = useState(false)
    return <>
        <Tooltip label="More settings">
            <ActionIcon variant="subtle" color="gray" aria-label="More settings" onClick={() => setOpen(true)}><IconAdjustments size={16}/></ActionIcon>
        </Tooltip>
        <Modal opened={open} onClose={() => setOpen(false)} title={`${k} settings`} size="sm">
            <Stack gap="sm">
                <ModelSelect label="Model" value={model} onChange={m => SetTaskModel(k, m).then(reload, errMsg)}/>
                <Text size="xs" c="dimmed">Used from the next run. Default: the agent's model, else the default_model setting.</Text>
                <RunnerSelect label="Runner" value={runner ?? ''} onChange={r => SetTaskRunner(k, r).then(reload, errMsg)}/>
                <Text size="xs" c="dimmed">Used from the next run. Default: the default_runner setting.</Text>
            </Stack>
        </Modal>
    </>
}

function Repos() {
    const {data, error, reload} = useData(ListRepos, [])
    const [path, setPath] = useState('')
    const [name, setName] = useState('')
    return (
        <Stack>
            <Text size="xs" c="dimmed">Each repo's setup script runs with bash in every new worktree before the agent starts (stdin closed, 10 min limit), e.g. to build a nix-direnv dev shell. A failure fails the launch and the script reruns on the next one. Log: workspaces/&lt;KEY&gt;/setup-&lt;repo&gt;.log.</Text>
            {error && <Text c="red">{error}</Text>}
            {data?.map(r => <RepoRow key={r.id} r={r} reload={reload}/>)}
            <Group align="flex-start" wrap="nowrap">
                <TextInput style={{flex: 2}} placeholder="/path/to/clone" value={path} onChange={e => setPath(e.target.value)}/>
                <TextInput style={{flex: 1}} placeholder="name (optional)" value={name} onChange={e => setName(e.target.value)}/>
                <Button onClick={() => AddRepo(path, name).then(() => { setPath(''); setName(''); reload() }, errMsg)}>Add</Button>
            </Group>
        </Stack>
    )
}

function RepoRow({r, reload}: {r: store.Repo; reload: () => void}) {
    const [val, setVal] = useState(r.setup_script)
    return (
        <Group wrap="nowrap" align="flex-end">
            <Textarea style={{flex: 1}} autosize minRows={2} maxRows={10} value={val} onChange={e => setVal(e.target.value)}
                      styles={{input: {fontFamily: 'var(--mantine-font-family-monospace)'}}}
                      label={<>{r.name} <Text span c="dimmed" size="xs">{r.default_branch} · {r.path}</Text></>}
                      placeholder="direnv allow . && direnv exec . true"/>
            <Button disabled={val === r.setup_script} onClick={() => SetRepoSetupScript(r.name, val).then(reload, errMsg)}>Save</Button>
        </Group>
    )
}

export type Field = {k: string; label: string; desc: string; kind?: "area" | "md" | "model" | "runner" | "secret"}

const TABS: {name: string; fields: Field[]}[] = [
    {name: "General", fields: [
        {k: "user_name", label: "Your name", desc: "Shown as the creator of tasks you create. Empty shows \"human\"."},
        {k: "key_prefix", label: "Task key prefix", desc: "Prefix for new task keys, e.g. CV gives CV-12."},
        {k: "default_model", label: "Default model", desc: "Model for runs that do not set one. Default uses the Claude Code default.", kind: "model"},
        {k: "default_runner", label: "Default runner", desc: "Runner backend for tasks that do not set one (claude, agy).", kind: "runner"},
    ]},
    {name: "Dispatch", fields: [
        {k: "max_parallel", label: "Max parallel runs", desc: "How many tasks run at once."},
        {k: "resume_threshold", label: "Resume threshold (tokens)", desc: "A hot session resumes while its context peak is below this; above it a fresh session starts."},
        {k: "comment_debounce_secs", label: "Comment debounce (s)", desc: "Wait this long after a comment before starting a run, so you can batch comments."},
        {k: "max_consecutive_failures", label: "Max consecutive failures", desc: "After this many failed runs in a row the task is marked failed."},
    ]},
    {name: "External terminal", fields: [
        {k: "terminal_template", label: "External terminal command", desc: `Run by the external terminal button (terminal icon) on the Shell, Resume and New session rows of a task. {cwd} is the folder and {cmd} the command to run. Platform defaults: macOS (Terminal.app via osascript), Linux (x-terminal-emulator), Windows (wt). macOS iTerm: osascript -e 'tell application "iTerm" to create window with default profile command "cd {cwd} && {cmd}"'. Ghostty: open -na Ghostty --args --working-directory={cwd} -e sh -c "{cmd}". (The internal terminal icon runs inside Conveyor and ignores this.)`, kind: "area"},
    ]},
    {name: "Prompts", fields: [
        {k: "pr_comment_prefix", label: "PR comment prefix", desc: "Every PR or issue comment the agent posts must start with this. Added to the brief and enforced by a hook. Empty disables both."},
        {k: "brief_extra_rules", label: "Extra rules", desc: "Markdown appended to the Rules section of every brief.", kind: "md"},
        {k: "prompt_fresh", label: "Fresh prompt", desc: "First prompt of a new session. Placeholders: {key} {title} {brief} {comments}. Empty uses the built-in.", kind: "area"},
        {k: "prompt_resume", label: "Resume prompt", desc: "Prompt that resumes a session with new comments. Same placeholders. Empty uses the built-in.", kind: "area"},
    ]},
]

/** Every setting label, for the command palette. */
export const SETTING_LABELS = TABS.flatMap(t => t.fields.map(f => f.label))

export function Settings() {
    const {data, error, reload} = useData(GetSettings, [])
    const {data: defs} = useData(GetPromptDefaults, [])
    const known = new Set(TABS.flatMap(t => t.fields.map(f => f.k)))
    const extra = Object.keys(data ?? {}).filter(k => !known.has(k)).sort().map((k): Field => ({k, label: k, desc: ""}))
    return (
        <Stack maw={800}>
            <Title order={3}>Settings</Title>
            {error && <Text c="red">{error}</Text>}
            {data && (
                <Tabs defaultValue="General" keepMounted={false}>
                    <Tabs.List mb="md">{TABS.map(t => <Tabs.Tab key={t.name} value={t.name}>{t.name}</Tabs.Tab>)}<Tabs.Tab value="Repos">Repos</Tabs.Tab></Tabs.List>
                    {TABS.map(t => (
                        <Tabs.Panel key={t.name} value={t.name}>
                            <Stack>
                                {(t.name === "General" ? [...t.fields, ...extra] : t.fields).map(f => <SettingRow key={f.k} f={f} v={data[f.k] ?? ""} reload={reload} ph={f.k === "prompt_fresh" ? defs?.fresh : f.k === "prompt_resume" ? defs?.resume : undefined}/>)}
                                {t.name === "General" && <StateButtons reload={reload}/>}
                                {t.name === "Prompts" && <PromptsInfo settings={data} protocol={defs?.protocol ?? ""}/>}
                            </Stack>
                        </Tabs.Panel>
                    ))}
                    <Tabs.Panel value="Repos"><Repos/></Tabs.Panel>
                </Tabs>
            )}
        </Stack>
    )
}

/** Export or import the portable state (settings, apps, automations, user agents) as a JSON file. */
function StateButtons({reload}: {reload: () => void}) {
    const [msg, setMsg] = useState<string[]>([])
    return (
        <Stack gap={4}>
            <Text size="sm" fw={500}>State</Text>
            <Text size="xs" c="dimmed">Move settings, apps, automations and user agents to another install. Repos are not exported: automations get placeholders and import disabled. Import only adds what is missing.</Text>
            <Group>
                <Button variant="default" onClick={() => ExportState().then(p => setMsg(p ? [`Exported to ${p}`] : []), errMsg)}>Export state</Button>
                <Button variant="default" onClick={() => ImportState().then(l => { setMsg(l ?? []); reload() }, errMsg)}>Import state</Button>
            </Group>
            {msg.map((l, i) => <Text key={i} size="sm">{l}</Text>)}
        </Stack>
    )
}

export function SettingRow({f, v, ph, reload}: {f: Field; v: string; ph?: string; reload: () => void}) {
    const [val, setVal] = useState(v)
    const props = {style: {flex: 1}, label: f.label, description: f.desc, placeholder: ph}
    return (
        <Group wrap="nowrap" align="flex-end">
            {f.kind === "md" ? <Stack gap={4} style={{flex: 1}}>
                <Text size="sm" fw={500}>{f.label}</Text><Text size="xs" c="dimmed">{f.desc}</Text>
                <MarkdownEditor minRows={4} value={val} onChange={setVal}/>
            </Stack>
                : f.kind === "model" ? <ModelSelect {...props} value={val} onChange={setVal}/>
                : f.kind === "runner" ? <RunnerSettingSelect {...props} value={val} onChange={setVal}/>
                : f.kind === "secret" ? <PasswordInput {...props} value={val} onChange={e => setVal(e.target.value)}/>
                : f.kind === "area" ? <Textarea {...props} autosize minRows={2} maxRows={10} value={val} onChange={e => setVal(e.target.value)}/>
                : <TextInput {...props} value={val} onChange={e => setVal(e.target.value)}/>}
            <Button disabled={val === v} onClick={() => SetSetting(f.k, val).then(reload, errMsg)}>Save</Button>
        </Group>
    )
}

/** What the agent receives: the built-in protocol (read-only) and a live preview of a task brief. */
function PromptsInfo({settings, protocol}: {settings: Record<string, string>; protocol: string}) {
    const {data: tasks} = useData(ListTasks, [])
    const [key, setKey] = useState<string | null>(null)
    const [raw, setRaw] = useState("rendered")
    const sig = JSON.stringify(settings) // refetch when any setting is saved
    const {data: pv, error} = useData(() => key ? PreviewBrief(key) : Promise.resolve(undefined), [key, sig])
    return (
        <Stack mt="md">
            <Title order={5}>Built-in protocol and rules</Title>
            <Text size="xs" c="dimmed">Always in the brief. Read-only; add to the rules with Extra rules above.</Text>
            <ScrollArea.Autosize mah={360}><Code block>{protocol}</Code></ScrollArea.Autosize>
            <Title order={5}>Preview</Title>
            <Text size="xs" c="dimmed">The brief and first prompt for a task as they would be sent now. Nothing is written.</Text>
            <Group>
                <Select style={{flex: 1}} placeholder="Pick a task" value={key} onChange={setKey}
                        data={(tasks ?? []).filter(t => t.status !== 'done' && t.status !== 'cancelled').sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)).map(t => ({value: t.key, label: `${t.key}  ${t.title}`}))}/>
                <SegmentedControl value={raw} onChange={setRaw} data={[{value: "rendered", label: "Rendered"}, {value: "raw", label: "Raw"}]}/>
            </Group>
            {error && <Text c="red">{error}</Text>}
            {pv && <>
                <Text size="sm" fw={600}>BRIEF.md</Text>
                {raw === "raw" ? <Code block>{pv.brief}</Code> : <Markdown>{pv.brief}</Markdown>}
                <Text size="sm" fw={600}>Fresh prompt</Text>
                <Code block>{pv.prompt}</Code>
            </>}
        </Stack>
    )
}
