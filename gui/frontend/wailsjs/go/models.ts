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

