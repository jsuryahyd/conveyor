export namespace core {
	
	export class Action {
	    label: string;
	    repo: string;
	    cwd: string;
	    cmd: string;
	    kind: string;
	
	    static createFrom(source: any = {}) {
	        return new Action(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.repo = source["repo"];
	        this.cwd = source["cwd"];
	        this.cmd = source["cmd"];
	        this.kind = source["kind"];
	    }
	}
	export class Agent {
	    name: string;
	    description: string;
	    scope: string;
	    model: string;
	
	    static createFrom(source: any = {}) {
	        return new Agent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.scope = source["scope"];
	        this.model = source["model"];
	    }
	}
	export class AppLog {
	    text: string;
	    next: number;
	
	    static createFrom(source: any = {}) {
	        return new AppLog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.next = source["next"];
	    }
	}
	export class NewTaskInput {
	    Title: string;
	    Description: string;
	    Repos: string[];
	    Primary: string;
	    Start: boolean;
	    Model: string;
	    Agent: string;
	    AgentSet: boolean;
	    Runner: string;
	    RunnerSet: boolean;
	    ParentKey: string;
	    Priority: number;
	    Source: string;
	    ExternalKey: string;
	    ExternalURL: string;
	
	    static createFrom(source: any = {}) {
	        return new NewTaskInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Title = source["Title"];
	        this.Description = source["Description"];
	        this.Repos = source["Repos"];
	        this.Primary = source["Primary"];
	        this.Start = source["Start"];
	        this.Model = source["Model"];
	        this.Agent = source["Agent"];
	        this.AgentSet = source["AgentSet"];
	        this.Runner = source["Runner"];
	        this.RunnerSet = source["RunnerSet"];
	        this.ParentKey = source["ParentKey"];
	        this.Priority = source["Priority"];
	        this.Source = source["Source"];
	        this.ExternalKey = source["ExternalKey"];
	        this.ExternalURL = source["ExternalURL"];
	    }
	}
	export class RunLog {
	    entries: transcript.Entry[];
	    next: number;
	
	    static createFrom(source: any = {}) {
	        return new RunLog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.entries = this.convertValues(source["entries"], transcript.Entry);
	        this.next = source["next"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Session {
	    id: string;
	    repo: string;
	    mtime: string;
	    prompt: string;
	    action: Action;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.repo = source["repo"];
	        this.mtime = source["mtime"];
	        this.prompt = source["prompt"];
	        this.action = this.convertValues(source["action"], Action);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace runner {
	
	export class CLIAgent {
	    id: string;
	    name: string;
	    command: string;
	    installed: boolean;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new CLIAgent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.command = source["command"];
	        this.installed = source["installed"];
	        this.path = source["path"];
	    }
	}

}

export namespace store {
	
	export class App {
	    id: number;
	    name: string;
	    command: string;
	    global: boolean;
	    output: boolean;
	
	    static createFrom(source: any = {}) {
	        return new App(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.command = source["command"];
	        this.global = source["global"];
	        this.output = source["output"];
	    }
	}
	export class Automation {
	    id: number;
	    name: string;
	    schedule: string;
	    precheck: string;
	    action_kind: string;
	    action_arg: string;
	    enabled: boolean;
	    last_run_at?: string;
	    last_result?: string;
	
	    static createFrom(source: any = {}) {
	        return new Automation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.schedule = source["schedule"];
	        this.precheck = source["precheck"];
	        this.action_kind = source["action_kind"];
	        this.action_arg = source["action_arg"];
	        this.enabled = source["enabled"];
	        this.last_run_at = source["last_run_at"];
	        this.last_result = source["last_result"];
	    }
	}
	export class Comment {
	    id: number;
	    task_id: number;
	    author: string;
	    body: string;
	    run_id?: number;
	    consumed_by_run_id?: number;
	    created_at: string;
	
	    static createFrom(source: any = {}) {
	        return new Comment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.task_id = source["task_id"];
	        this.author = source["author"];
	        this.body = source["body"];
	        this.run_id = source["run_id"];
	        this.consumed_by_run_id = source["consumed_by_run_id"];
	        this.created_at = source["created_at"];
	    }
	}
	export class TaskRef {
	    key: string;
	    title: string;
	    status: string;
	
	    static createFrom(source: any = {}) {
	        return new TaskRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.title = source["title"];
	        this.status = source["status"];
	    }
	}
	export class Relation {
	    kind: string;
	    label: string;
	    from: boolean;
	    task: TaskRef;
	
	    static createFrom(source: any = {}) {
	        return new Relation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.from = source["from"];
	        this.task = this.convertValues(source["task"], TaskRef);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Relations {
	    parent?: TaskRef;
	    children: TaskRef[];
	    links: Relation[];
	    blocked_by_all: string[];
	    blocks_all: string[];
	    ancestors: string[];
	    descendants: string[];
	
	    static createFrom(source: any = {}) {
	        return new Relations(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.parent = this.convertValues(source["parent"], TaskRef);
	        this.children = this.convertValues(source["children"], TaskRef);
	        this.links = this.convertValues(source["links"], Relation);
	        this.blocked_by_all = source["blocked_by_all"];
	        this.blocks_all = source["blocks_all"];
	        this.ancestors = source["ancestors"];
	        this.descendants = source["descendants"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Repo {
	    id: number;
	    name: string;
	    path: string;
	    default_branch: string;
	    created_at: string;
	
	    static createFrom(source: any = {}) {
	        return new Repo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.default_branch = source["default_branch"];
	        this.created_at = source["created_at"];
	    }
	}
	export class Run {
	    id: number;
	    task_id: number;
	    kind: string;
	    mode: string;
	    bg_id?: string;
	    session_id?: string;
	    status: string;
	    prompt: string;
	    block_status?: string;
	    summary?: string;
	    state?: string;
	    next?: string;
	    criteria?: string;
	    final_message?: string;
	    tokens_in: number;
	    tokens_out: number;
	    cache_read: number;
	    cache_write: number;
	    ctx_peak: number;
	    api_calls: number;
	    error?: string;
	    started_at: string;
	    ended_at?: string;
	
	    static createFrom(source: any = {}) {
	        return new Run(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.task_id = source["task_id"];
	        this.kind = source["kind"];
	        this.mode = source["mode"];
	        this.bg_id = source["bg_id"];
	        this.session_id = source["session_id"];
	        this.status = source["status"];
	        this.prompt = source["prompt"];
	        this.block_status = source["block_status"];
	        this.summary = source["summary"];
	        this.state = source["state"];
	        this.next = source["next"];
	        this.criteria = source["criteria"];
	        this.final_message = source["final_message"];
	        this.tokens_in = source["tokens_in"];
	        this.tokens_out = source["tokens_out"];
	        this.cache_read = source["cache_read"];
	        this.cache_write = source["cache_write"];
	        this.ctx_peak = source["ctx_peak"];
	        this.api_calls = source["api_calls"];
	        this.error = source["error"];
	        this.started_at = source["started_at"];
	        this.ended_at = source["ended_at"];
	    }
	}
	export class TaskRepo {
	    task_id: number;
	    repo_id: number;
	    repo_name: string;
	    is_primary: boolean;
	    branch: string;
	    worktree_path?: string;
	    pr_url?: string;
	    pr_state?: string;
	
	    static createFrom(source: any = {}) {
	        return new TaskRepo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.task_id = source["task_id"];
	        this.repo_id = source["repo_id"];
	        this.repo_name = source["repo_name"];
	        this.is_primary = source["is_primary"];
	        this.branch = source["branch"];
	        this.worktree_path = source["worktree_path"];
	        this.pr_url = source["pr_url"];
	        this.pr_state = source["pr_state"];
	    }
	}
	export class Task {
	    id: number;
	    key: string;
	    title: string;
	    description: string;
	    status: string;
	    priority: number;
	    model?: string;
	    agent?: string;
	    runner?: string;
	    source: string;
	    external_key?: string;
	    external_url?: string;
	    parent_id?: number;
	    consecutive_failures: number;
	    created_at: string;
	    updated_at: string;
	    repos?: TaskRepo[];
	
	    static createFrom(source: any = {}) {
	        return new Task(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.key = source["key"];
	        this.title = source["title"];
	        this.description = source["description"];
	        this.status = source["status"];
	        this.priority = source["priority"];
	        this.model = source["model"];
	        this.agent = source["agent"];
	        this.runner = source["runner"];
	        this.source = source["source"];
	        this.external_key = source["external_key"];
	        this.external_url = source["external_url"];
	        this.parent_id = source["parent_id"];
	        this.consecutive_failures = source["consecutive_failures"];
	        this.created_at = source["created_at"];
	        this.updated_at = source["updated_at"];
	        this.repos = this.convertValues(source["repos"], TaskRepo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	

}

export namespace transcript {
	
	export class Entry {
	    kind: string;
	    text: string;
	    at: string;
	
	    static createFrom(source: any = {}) {
	        return new Entry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.text = source["text"];
	        this.at = source["at"];
	    }
	}

}

export namespace ui {
	
	export class BriefPreview {
	    brief: string;
	    prompt: string;
	
	    static createFrom(source: any = {}) {
	        return new BriefPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.brief = source["brief"];
	        this.prompt = source["prompt"];
	    }
	}
	export class LearningRow {
	    id: number;
	    task_id?: number;
	    run_id?: number;
	    repo_id?: number;
	    body: string;
	    status: string;
	    target?: string;
	    applied_task_id?: number;
	    created_at: string;
	    repo: string;
	
	    static createFrom(source: any = {}) {
	        return new LearningRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.task_id = source["task_id"];
	        this.run_id = source["run_id"];
	        this.repo_id = source["repo_id"];
	        this.body = source["body"];
	        this.status = source["status"];
	        this.target = source["target"];
	        this.applied_task_id = source["applied_task_id"];
	        this.created_at = source["created_at"];
	        this.repo = source["repo"];
	    }
	}
	export class PromptDefaults {
	    protocol: string;
	    fresh: string;
	    resume: string;
	
	    static createFrom(source: any = {}) {
	        return new PromptDefaults(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.protocol = source["protocol"];
	        this.fresh = source["fresh"];
	        this.resume = source["resume"];
	    }
	}
	export class ScheduleInfo {
	    valid: boolean;
	    error: string;
	    description: string;
	    next: string[];
	
	    static createFrom(source: any = {}) {
	        return new ScheduleInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.valid = source["valid"];
	        this.error = source["error"];
	        this.description = source["description"];
	        this.next = source["next"];
	    }
	}
	export class TaskCard {
	    key: string;
	    title: string;
	    status: string;
	    priority: number;
	    parent: string;
	    repos: string[];
	    runner: string;
	    lastSummary: string;
	    ctxPeak: number;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new TaskCard(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.title = source["title"];
	        this.status = source["status"];
	        this.priority = source["priority"];
	        this.parent = source["parent"];
	        this.repos = source["repos"];
	        this.runner = source["runner"];
	        this.lastSummary = source["lastSummary"];
	        this.ctxPeak = source["ctxPeak"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class TaskDetail {
	    task: store.Task;
	    comments: store.Comment[];
	    runs: store.Run[];
	    relations: store.Relations;
	
	    static createFrom(source: any = {}) {
	        return new TaskDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.task = this.convertValues(source["task"], store.Task);
	        this.comments = this.convertValues(source["comments"], store.Comment);
	        this.runs = this.convertValues(source["runs"], store.Run);
	        this.relations = this.convertValues(source["relations"], store.Relations);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TermBuf {
	    data: string;
	    end: number;
	
	    static createFrom(source: any = {}) {
	        return new TermBuf(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.data = source["data"];
	        this.end = source["end"];
	    }
	}
	export class TermInfo {
	    id: string;
	    title: string;
	    exited: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TermInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.exited = source["exited"];
	    }
	}

}

