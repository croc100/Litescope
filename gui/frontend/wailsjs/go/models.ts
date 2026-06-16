export namespace check {
	
	export class Result {
	    Path: string;
	    IntegrityOK: boolean;
	    ""?: diff.Result;
	    Passed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Result(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Path = source["Path"];
	        this.IntegrityOK = source["IntegrityOK"];
	        this[""] = this.convertValues(source[""], diff.Result);
	        this.Passed = source["Passed"];
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
	export class TableStat {
	    Name: string;
	    BackupRows: number;
	
	    static createFrom(source: any = {}) {
	        return new TableStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.BackupRows = source["BackupRows"];
	    }
	}

}

export namespace diff {
	
	export class ColumnChange {
	    Name: string;
	    Old?: schema.Column;
	    New?: schema.Column;
	
	    static createFrom(source: any = {}) {
	        return new ColumnChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Old = this.convertValues(source["Old"], schema.Column);
	        this.New = this.convertValues(source["New"], schema.Column);
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
	export class DataDiff {
	    Table: string;
	    Added: number;
	    Removed: number;
	    Changed: number;
	
	    static createFrom(source: any = {}) {
	        return new DataDiff(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Table = source["Table"];
	        this.Added = source["Added"];
	        this.Removed = source["Removed"];
	        this.Changed = source["Changed"];
	    }
	}
	export class TableDiff {
	    Name: string;
	    Added: boolean;
	    Removed: boolean;
	    AddedColumns: schema.Column[];
	    RemovedColumns: schema.Column[];
	    ChangedColumns: ColumnChange[];
	    AddedIndexes: schema.Index[];
	    RemovedIndexes: schema.Index[];
	
	    static createFrom(source: any = {}) {
	        return new TableDiff(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Added = source["Added"];
	        this.Removed = source["Removed"];
	        this.AddedColumns = this.convertValues(source["AddedColumns"], schema.Column);
	        this.RemovedColumns = this.convertValues(source["RemovedColumns"], schema.Column);
	        this.ChangedColumns = this.convertValues(source["ChangedColumns"], ColumnChange);
	        this.AddedIndexes = this.convertValues(source["AddedIndexes"], schema.Index);
	        this.RemovedIndexes = this.convertValues(source["RemovedIndexes"], schema.Index);
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
	export class Result {
	    Schema: TableDiff[];
	    Data: DataDiff[];
	
	    static createFrom(source: any = {}) {
	        return new Result(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Schema = this.convertValues(source["Schema"], TableDiff);
	        this.Data = this.convertValues(source["Data"], DataDiff);
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

export namespace main {

	export class FleetDBEntry {
	    name: string;
	    dsn: string;
	    tags?: string[];
	    static createFrom(source: any = {}) { return new FleetDBEntry(source); }
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"]; this.dsn = source["dsn"]; this.tags = source["tags"];
	    }
	}

	export class FleetCheckResult {
	    database: string;
	    state: string;
	    error?: string;
	    changes: number;
	    duration_ms: number;
	    static createFrom(source: any = {}) { return new FleetCheckResult(source); }
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.database = source["database"]; this.state = source["state"];
	        this.error = source["error"]; this.changes = source["changes"]; this.duration_ms = source["duration_ms"];
	    }
	}

	export class FleetSnapshotResult {
	    database: string;
	    tables: number;
	    error?: string;
	    static createFrom(source: any = {}) { return new FleetSnapshotResult(source); }
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.database = source["database"]; this.tables = source["tables"]; this.error = source["error"];
	    }
	}

	export class DiffedRow {
	    Status: string;
	    PK: any;
	    Old: Record<string, any>;
	    New: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new DiffedRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Status = source["Status"];
	        this.PK = source["PK"];
	        this.Old = source["Old"];
	        this.New = source["New"];
	    }
	}
	export class MigrateApplyResult {
	    Executed: number;
	    BackupPath: string;
	    DryRun: boolean;
	    DurationMs: number;
	
	    static createFrom(source: any = {}) {
	        return new MigrateApplyResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Executed = source["Executed"];
	        this.BackupPath = source["BackupPath"];
	        this.DryRun = source["DryRun"];
	        this.DurationMs = source["DurationMs"];
	    }
	}
	export class MigratePreview {
	    SQL: string;
	    Warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new MigratePreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SQL = source["SQL"];
	        this.Warnings = source["Warnings"];
	    }
	}
	export class SnapshotInfo {
	    Source: string;
	    CapturedAt: string;
	    TableCount: number;
	
	    static createFrom(source: any = {}) {
	        return new SnapshotInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Source = source["Source"];
	        this.CapturedAt = source["CapturedAt"];
	        this.TableCount = source["TableCount"];
	    }
	}
	export class TableRows {
	    Columns: string[];
	    Rows: any[][];
	    Total: number;
	
	    static createFrom(source: any = {}) {
	        return new TableRows(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Columns = source["Columns"];
	        this.Rows = source["Rows"];
	        this.Total = source["Total"];
	    }
	}

}

export namespace monitor {
	
	export class DriftResult {
	    source: string;
	    // Go type: time
	    baseline_at: any;
	    // Go type: time
	    checked_at: any;
	    has_drift: boolean;
	    changes?: diff.TableDiff[];
	
	    static createFrom(source: any = {}) {
	        return new DriftResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	        this.baseline_at = this.convertValues(source["baseline_at"], null);
	        this.checked_at = this.convertValues(source["checked_at"], null);
	        this.has_drift = source["has_drift"];
	        this.changes = this.convertValues(source["changes"], diff.TableDiff);
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
	export class HistoryEntry {
	    source: string;
	    // Go type: time
	    baseline_at: any;
	    // Go type: time
	    checked_at: any;
	    has_drift: boolean;
	    changes?: diff.TableDiff[];
	
	    static createFrom(source: any = {}) {
	        return new HistoryEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	        this.baseline_at = this.convertValues(source["baseline_at"], null);
	        this.checked_at = this.convertValues(source["checked_at"], null);
	        this.has_drift = source["has_drift"];
	        this.changes = this.convertValues(source["changes"], diff.TableDiff);
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

export namespace schema {
	
	export class Column {
	    Name: string;
	    Type: string;
	    NotNull: boolean;
	    Default: string;
	    PK: number;
	
	    static createFrom(source: any = {}) {
	        return new Column(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Type = source["Type"];
	        this.NotNull = source["NotNull"];
	        this.Default = source["Default"];
	        this.PK = source["PK"];
	    }
	}
	export class Index {
	    Name: string;
	    Table: string;
	    Unique: boolean;
	    SQL: string;
	
	    static createFrom(source: any = {}) {
	        return new Index(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Table = source["Table"];
	        this.Unique = source["Unique"];
	        this.SQL = source["SQL"];
	    }
	}
	export class Table {
	    Name: string;
	    Columns: Column[];
	    Indexes: Index[];
	
	    static createFrom(source: any = {}) {
	        return new Table(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Columns = this.convertValues(source["Columns"], Column);
	        this.Indexes = this.convertValues(source["Indexes"], Index);
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
	export class Schema {
	    Tables: Table[];
	
	    static createFrom(source: any = {}) {
	        return new Schema(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Tables = this.convertValues(source["Tables"], Table);
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

