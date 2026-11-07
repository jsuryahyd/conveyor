import {useState} from 'react'
import {ActionIcon, Anchor, Badge, Button, Code, Divider, Group, Menu, Modal, Paper, Stack, TagsInput, Text, TextInput, Title, Tooltip} from '@mantine/core'
import {useClipboard} from '@mantine/hooks'
import {IconCopy, IconMessageSearch, IconPencil, IconPlus, IconStar, IconX, IconTerminal, IconTerminal2} from '@tabler/icons-react'
import {Actions, AddLabel, AddTaskRepo, CheckReviews, EnsureWorkspace, GetSettings, ListLabels, ListRepos, RemoveLabel, RemoveTaskRepo, RunAction, Sessions, SetTaskRepo, TermStart} from '../wailsjs/go/ui/App'
import {core, store, ui} from '../wailsjs/go/models'
import {BrowserOpenURL} from '../wailsjs/runtime/runtime'
import {PlacedButtons, StatusSelect, Time, errMsg} from './components'
import {TaskAgent, TaskSettings} from './forms'
import {useData} from './util'

/** Confirmation popup: renders while `msg` is set; run `ok` on confirm, `close` on cancel. */
function Confirm({msg, ok, close}: {msg: string | null; ok: () => void; close: () => void}) {
    return (
        <Modal opened={!!msg} onClose={close} title="Please confirm" size="sm" centered>
            <Text size="sm">{msg}</Text>
            <Group justify="flex-end" mt="md">
                <Button variant="default" onClick={close}>Cancel</Button>
                <Button onClick={() => { close(); ok() }}>Confirm</Button>
            </Group>
        </Modal>
    )
}

const copy = (s: string) => navigator.clipboard.writeText(s).catch(errMsg)
const sq = (s: string) => "'" + s.replace(/'/g, "'\\''") + "'"
const cdCmd = (a: core.Action) => 'cd ' + sq(a.cwd) + (a.cmd ? ' && ' + a.cmd : '')

/** One full-width row: label left, copy / external terminal / internal terminal icons right. */
function ActionRow({k, a, kind, onTerm}: {k: string; a: core.Action; kind?: string; onTerm: (t: ui.TermInfo) => void}) {
    return (
        <Group justify="space-between" wrap="nowrap" gap="xs">
            <Text size="sm" style={{wordBreak: 'break-word'}}>{a.label}</Text>
            <ActionIcons k={k} a={a} kind={kind} onTerm={onTerm}/>
        </Group>
    )
}

/** Copy / external terminal / internal terminal icons for one action. */
function ActionIcons({k, a, kind, onTerm}: {k: string; a: core.Action; kind?: string; onTerm: (t: ui.TermInfo) => void}) {
    return (
        <>
            <Group gap={2} wrap="nowrap" mr={6}>
                <Tooltip label="Copy command">
                    <ActionIcon variant="default" size="sm" aria-label="Copy command" onClick={() => copy(cdCmd(a))}><IconCopy size={14}/></ActionIcon>
                </Tooltip>
                <Tooltip label="Open in external terminal">
                    <ActionIcon variant="default" size="sm" aria-label="Open in external terminal" onClick={() => RunAction(k, kind ?? a.kind, a.repo).catch(errMsg)}><IconTerminal2 size={14}/></ActionIcon>
                </Tooltip>
                <Tooltip label="Open in internal terminal">
                    <ActionIcon variant="default" size="sm" aria-label="Open in internal terminal"
                                onClick={() => TermStart(k, kind ?? a.kind, a.repo).then(onTerm).catch(errMsg)}><IconTerminal size={14}/></ActionIcon>
                </Tooltip>
            </Group>
        </>
    )
}

const PR_COLOR: Record<string, string> = {open: 'green', merged: 'violet', closed: 'red', draft: 'gray'}

/** Full value, wrapped anywhere; click copies it and flashes "Copied". */
function Mono({text}: {text: string}) {
    const cb = useClipboard({timeout: 1200})
    return (
        <Tooltip label="Copied" opened={cb.copied} position="top-start">
            <Code style={{display: 'block', cursor: 'pointer', whiteSpace: 'pre-wrap', wordBreak: 'break-all'}} title="Click to copy" onClick={() => cb.copy(text)}>{text}</Code>
        </Tooltip>
    )
}

// RepoFields shows one repo's branch, worktree and PR. The branch is editable
// until the worktree exists; the PR URL via the pencil.
function RepoFields({k, r, reload, canRemove, children}: {k: string; r: store.TaskRepo; reload: () => void; canRemove?: boolean; children?: React.ReactNode}) {
    const [branch, setBranch] = useState(r.branch)
    const [pr, setPr] = useState(r.pr_url ?? '')
    const [editPr, setEditPr] = useState(false)
    const [rm, setRm] = useState(false)
    const dirty = branch !== r.branch || pr !== (r.pr_url ?? '')
    const save = () => SetTaskRepo(k, r.repo_name, branch, pr).then(reload).catch(errMsg)
    const onKey = (e: React.KeyboardEvent) => { if (e.key === 'Enter' && dirty) save() }
    return (
        <Paper withBorder p="xs" radius="md">
            <Confirm msg={rm ? `Remove ${r.repo_name} from ${k}?` : null} close={() => setRm(false)}
                     ok={() => RemoveTaskRepo(k, r.repo_name).then(reload).catch(errMsg)}/>
            <Stack gap="xs">
                <Group gap={6} justify="space-between" wrap="nowrap">
                    <Group gap={6}>
                        <Text size="sm" fw={600}>{r.repo_name}</Text>
                        {r.is_primary && <Badge size="xs" variant="light" leftSection={<IconStar size={10}/>}>primary</Badge>}
                    </Group>
                    {canRemove && !r.pr_url && (
                        <Tooltip label="Remove repo">
                            <ActionIcon variant="subtle" color="gray" size="xs" aria-label={'Remove ' + r.repo_name}
                                        onClick={() => setRm(true)}><IconX size={12}/></ActionIcon>
                        </Tooltip>
                    )}
                </Group>
                <div>
                    <Text size="xs" c="dimmed">Branch</Text>
                    {r.worktree_path
                        ? <Mono text={r.branch}/>
                        : <TextInput size="xs" value={branch} onChange={e => setBranch(e.target.value)} onKeyDown={onKey}/>}
                </div>
                <div>
                    <Text size="xs" c="dimmed">Worktree</Text>
                    {r.worktree_path ? <Mono text={r.worktree_path}/> : <Text size="xs" c="dimmed">not created yet</Text>}
                </div>
                <div>
                    <Group gap={6} mb={2}>
                        <Text size="xs" c="dimmed">PR</Text>
                        {r.pr_url && r.pr_state && <Badge size="xs" variant="light" color={PR_COLOR[r.pr_state.toLowerCase()] ?? 'gray'}>{r.pr_state}</Badge>}
                        {r.pr_url && <Anchor size="xs" href={r.pr_url} onClick={e => { e.preventDefault(); BrowserOpenURL(r.pr_url!) }}>Open PR</Anchor>}
                        {r.pr_state === 'OPEN' && (
                            <Tooltip label="Check reviews now">
                                <ActionIcon variant="subtle" color="gray" size="xs" aria-label="Check reviews" onClick={() => CheckReviews(k).catch(errMsg)}><IconMessageSearch size={12}/></ActionIcon>
                            </Tooltip>
                        )}
                        <Tooltip label="Edit PR URL">
                            <ActionIcon variant="subtle" color="gray" size="xs" aria-label="Edit PR URL" onClick={() => setEditPr(!editPr)}><IconPencil size={12}/></ActionIcon>
                        </Tooltip>
                    </Group>
                    {editPr || !r.pr_url
                        ? <TextInput size="xs" value={pr} placeholder="https://github.com/…/pull/1" onChange={e => setPr(e.target.value)} onKeyDown={onKey}/>
                        : <Mono text={r.pr_url}/>}
                </div>
                {dirty && <Button size="compact-sm" onClick={save}>Save</Button>}
                {children}
            </Stack>
        </Paper>
    )
}

/** Who made the task: the exact created_by, else (rows from before it existed) inferred from the lettered key (CV-14A is a child of CV-14). */
function creator(key: string, source: string, user: string | undefined, createdBy?: string | null) {
    if (createdBy) {
        const [kind, ...rest] = createdBy.split(':'), arg = rest.join(':')
        if (kind === 'human') return user || 'human'
        if (kind === 'agent') return `agent · ${arg}`
        if (kind === 'followup') return `follow-up of ${arg}`
        if (kind === 'automation') return `automation · ${arg}`
        return createdBy
    }
    const parent = key.match(/^(.+-\d+)[A-Z]+$/)?.[1]
    if (parent) return source === 'followup' ? `follow-up of ${parent}` : `agent · ${parent}`
    if (source === 'jira' || source === 'learning') return source
    return user || 'human'
}

/** Label picker: choose existing labels from the dropdown or type a new one and press Enter. */
function Labels({k, labels, reload}: {k: string; labels: string[]; reload: () => void}) {
    const {data: all, reload: reloadAll} = useData(ListLabels, [])
    const change = (next: string[]) => {
        const lower = (xs: string[]) => xs.map(x => x.toLowerCase())
        const adds = next.filter(n => !lower(labels).includes(n.toLowerCase()))
        const drops = labels.filter(l => !lower(next).includes(l.toLowerCase()))
        Promise.all([...adds.map(n => AddLabel(k, n)), ...drops.map(n => RemoveLabel(k, n))]).then(() => { reload(); reloadAll() }).catch(errMsg)
    }
    return <TagsInput size="xs" label="Labels" placeholder="Add label…" value={labels} data={all ?? []} onChange={change} clearable={false} splitChars={[',']}/>
}

/** + button: add a registered repo the task does not have yet. */
function AddRepo({k, have, reload}: {k: string; have: string[]; reload: () => void}) {
    const {data: all} = useData(ListRepos, [])
    const rest = (all ?? []).filter(r => !have.includes(r.name))
    const [pick, setPick] = useState<string | null>(null)
    return (<>
        <Confirm msg={pick && `Add ${pick} to ${k}?`} close={() => setPick(null)}
                 ok={() => AddTaskRepo(k, pick!).then(reload).catch(errMsg)}/>
        <Menu position="bottom-end" withinPortal>
            <Menu.Target>
                <Tooltip label="Add repo"><ActionIcon variant="subtle" size="sm" aria-label="Add repo"><IconPlus size={14}/></ActionIcon></Tooltip>
            </Menu.Target>
            <Menu.Dropdown>
                {rest.length === 0 && <Menu.Label>No other repos registered</Menu.Label>}
                {rest.map(r => <Menu.Item key={r.name} onClick={() => setPick(r.name)}>{r.name}</Menu.Item>)}
            </Menu.Dropdown>
        </Menu>
    </>)
}

export function TaskSidebar({t, runs, reload, onTerm}: {t: store.Task; runs: store.Run[]; reload: () => void; onTerm: (t: ui.TermInfo) => void}) {
    const k = t.key
    const repos = t.repos ?? []
    const {data: actions, error: actionsError, reload: reloadActions} = useData(() => Actions(k), [k])
    const {data: sessions, error: sessionsError, reload: reloadSessions} = useData(() => Sessions(k), [k])
    const {data: settings} = useData(GetSettings, [])
    const by = creator(k, t.source, settings?.user_name, t.created_by)
    const [busy, setBusy] = useState(false)
    const taskActions = (actions ?? []).filter(a => ['attach', 'fresh', 'shell'].includes(a.kind))
    const resume = new Map((actions ?? []).filter(a => /^(resume|attach):/.test(a.kind)).map(a => [a.kind.split(':')[1], a]))
    const hasWorktree = repos.some(r => r.worktree_path)
    return (
        <Stack gap="sm" component="aside" miw={0} pr="xs">
            <Group justify="space-between" wrap="nowrap">
                <Group gap={4}><Text size="sm" fw={600}>Agent</Text><TaskSettings k={k} model={t.model ?? ''} runner={t.runner ?? ''} reload={reload}/></Group>
                <TaskAgent k={k} repos={repos.map(r => r.repo_name)} agent={t.agent ?? ''}/>
            </Group>
            <Group justify="space-between">
                <Text size="sm" fw={600}>Status</Text>
                <StatusSelect k={k} status={t.status}/>
            </Group>
            <Group justify="space-between"><Text size="xs" c="dimmed">Created</Text><Group gap={4} wrap="nowrap"><Text size="xs">{by} ·</Text><Time iso={t.created_at}/></Group></Group>
            <Group justify="space-between"><Text size="xs" c="dimmed">Updated</Text><Time iso={t.updated_at}/></Group>
            <Labels k={k} labels={t.labels ?? []} reload={reload}/>
            <Divider/>
            <Group justify="space-between">
                <Title order={6}>Repos</Title>
                <AddRepo k={k} have={repos.map(r => r.repo_name)} reload={reload}/>
            </Group>
            {actionsError && <Text size="xs" c="red">Failed to load actions: {actionsError}</Text>}
            {repos.map(r => (
                <RepoFields key={r.repo_id + '|' + r.branch + '|' + (r.pr_url ?? '')} k={k} r={r} reload={reload} canRemove={repos.length > 1}/>
            ))}
            {!hasWorktree && (
                <Button loading={busy} onClick={() => { setBusy(true); EnsureWorkspace(k).then(() => { reload(); reloadActions(); reloadSessions() }).catch(errMsg).finally(() => setBusy(false)) }}>Create workspace</Button>
            )}
            {hasWorktree && <PlacedButtons place="task_sidebar" k={k}/>}
            {taskActions.map(a => <ActionRow key={a.kind + a.repo} k={k} a={a} onTerm={onTerm}/>)}
            <Divider/>
            <Title order={6}>Sessions</Title>
            {sessionsError && <Text size="xs" c="red">Failed to load sessions: {sessionsError}</Text>}
            {runs.length + (sessions?.length ?? 0) === 0 && <Text size="xs" c="dimmed">None yet.</Text>}
            {runs.map((r, i) => (
                <Group key={r.id} justify="space-between" wrap="nowrap" gap="xs">
                    <Anchor size="xs" c="dimmed" component="button" ta="left" title={r.session_id ? `Session ${r.session_id}` : 'Show in thread'}
                            onClick={() => document.getElementById('run-' + r.id)?.scrollIntoView({behavior: 'smooth', block: 'start'})}>
                        #{i + 1} {r.kind} · {r.status}{r.block_status ? ' · ' + r.block_status : ''}
                    </Anchor>
                    {resume.get(String(r.id)) && <ActionIcons k={k} a={resume.get(String(r.id))!} onTerm={onTerm}/>}
                </Group>
            ))}
            {sessions?.map(s => (
                <Stack key={s.id} gap={2}>
                    <ActionRow k={k} a={s.action} kind={'resume:' + s.id} onTerm={onTerm}/>
                    <Text size="xs" c="dimmed" lineClamp={1} title={`Session ${s.id}\n${s.prompt}`}>{s.prompt || s.id}</Text>
                </Stack>
            ))}
        </Stack>
    )
}
