export namespace main {
	
	export class Reply {
	    result?: Record<string, any>;
	    error?: model.Error;
	
	    static createFrom(source: any = {}) {
	        return new Reply(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.result = source["result"];
	        this.error = this.convertValues(source["error"], model.Error);
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

export namespace model {
	
	export class Error {
	    releaseURL?: string;
	    clientVersion?: string;
	    serverVersion?: string;
	    upgradeTarget?: string;
	    code: string;
	    message: string;
	    retryable: boolean;
	    operation?: string;
	    detail?: string;
	    httpStatus?: number;
	    remoteCode?: string;
	    requestId?: string;
	
	    static createFrom(source: any = {}) {
	        return new Error(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.releaseURL = source["releaseURL"];
	        this.clientVersion = source["clientVersion"];
	        this.serverVersion = source["serverVersion"];
	        this.upgradeTarget = source["upgradeTarget"];
	        this.code = source["code"];
	        this.message = source["message"];
	        this.retryable = source["retryable"];
	        this.operation = source["operation"];
	        this.detail = source["detail"];
	        this.httpStatus = source["httpStatus"];
	        this.remoteCode = source["remoteCode"];
	        this.requestId = source["requestId"];
	    }
	}

}

