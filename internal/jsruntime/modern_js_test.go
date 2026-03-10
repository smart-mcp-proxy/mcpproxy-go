package jsruntime

import (
	"context"
	"testing"
)

// TestModernJSArrowFunctions verifies arrow function syntax works
func TestModernJSArrowFunctions(t *testing.T) {
	caller := newMockToolCaller()

	tests := []struct {
		name string
		code string
	}{
		{
			"basic arrow function",
			`const double = (x) => x * 2; ({ result: double(21) })`,
		},
		{
			"arrow with implicit return",
			`const items = [1, 2, 3]; ({ result: items.map(x => x * 2) })`,
		},
		{
			"arrow with block body",
			`const greet = (name) => { return "Hello, " + name; }; ({ result: greet("world") })`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Execute(context.Background(), caller, tt.code, ExecutionOptions{})
			if !result.Ok {
				t.Fatalf("expected ok=true, got error: %v", result.Error)
			}
		})
	}
}

// TestModernJSConstLet verifies const and let declarations work
func TestModernJSConstLet(t *testing.T) {
	caller := newMockToolCaller()

	tests := []struct {
		name string
		code string
	}{
		{
			"const declaration",
			`const x = 42; ({ result: x })`,
		},
		{
			"let declaration",
			`let x = 10; x = 20; ({ result: x })`,
		},
		{
			"block scoping with let",
			`let x = 1; { let x = 2; } ({ result: x })`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Execute(context.Background(), caller, tt.code, ExecutionOptions{})
			if !result.Ok {
				t.Fatalf("expected ok=true, got error: %v", result.Error)
			}
		})
	}
}

// TestModernJSTemplateLiterals verifies template literal syntax works
func TestModernJSTemplateLiterals(t *testing.T) {
	caller := newMockToolCaller()

	code := "const name = 'world'; ({ result: `Hello, ${name}!` })"
	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["result"] != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got %v", resultMap["result"])
	}
}

// TestModernJSDestructuring verifies destructuring assignment works
func TestModernJSDestructuring(t *testing.T) {
	caller := newMockToolCaller()

	tests := []struct {
		name string
		code string
	}{
		{
			"object destructuring",
			`const { a, b } = { a: 1, b: 2 }; ({ result: a + b })`,
		},
		{
			"array destructuring",
			`const [x, y, z] = [10, 20, 30]; ({ result: x + y + z })`,
		},
		{
			"nested destructuring",
			`const { user: { name } } = { user: { name: "Alice" } }; ({ result: name })`,
		},
		{
			"destructuring with defaults",
			`const { a = 1, b = 2 } = { a: 10 }; ({ result: a + b })`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Execute(context.Background(), caller, tt.code, ExecutionOptions{})
			if !result.Ok {
				t.Fatalf("expected ok=true, got error: %v", result.Error)
			}
		})
	}
}

// TestModernJSSpreadRest verifies spread and rest operators work
func TestModernJSSpreadRest(t *testing.T) {
	caller := newMockToolCaller()

	tests := []struct {
		name string
		code string
	}{
		{
			"array spread",
			`const a = [1, 2]; const b = [...a, 3, 4]; ({ result: b.length })`,
		},
		{
			"object spread",
			`const a = { x: 1 }; const b = { ...a, y: 2 }; ({ result: b.x + b.y })`,
		},
		{
			"rest parameters",
			`function sum(...nums) { return nums.reduce((a, b) => a + b, 0); } ({ result: sum(1, 2, 3, 4) })`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Execute(context.Background(), caller, tt.code, ExecutionOptions{})
			if !result.Ok {
				t.Fatalf("expected ok=true, got error: %v", result.Error)
			}
		})
	}
}

// TestModernJSDefaultParameters verifies default parameter values work
func TestModernJSDefaultParameters(t *testing.T) {
	caller := newMockToolCaller()

	code := `function greet(name = "world") { return "Hello, " + name; } ({ result: greet() })`
	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["result"] != "Hello, world" {
		t.Errorf("expected 'Hello, world', got %v", resultMap["result"])
	}
}

// TestModernJSClasses verifies ES6 class syntax works
func TestModernJSClasses(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		class Animal {
			constructor(name) {
				this.name = name;
			}
			speak() {
				return this.name + " makes a sound";
			}
		}

		class Dog extends Animal {
			speak() {
				return this.name + " barks";
			}
		}

		const dog = new Dog("Rex");
		({ result: dog.speak() })
	`
	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["result"] != "Rex barks" {
		t.Errorf("expected 'Rex barks', got %v", resultMap["result"])
	}
}

// TestModernJSForOf verifies for-of loop works
func TestModernJSForOf(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		const items = [10, 20, 30];
		let sum = 0;
		for (const item of items) {
			sum += item;
		}
		({ result: sum })
	`
	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	sumVal := toInt64(resultMap["result"])
	if sumVal != 60 {
		t.Errorf("expected 60, got %v", resultMap["result"])
	}
}

// TestModernJSPromises verifies Promise support works
func TestModernJSPromises(t *testing.T) {
	caller := newMockToolCaller()

	// Goja supports Promise constructor but execution is synchronous
	code := `
		const p = new Promise((resolve) => {
			resolve(42);
		});
		({ result: typeof Promise !== 'undefined' })
	`
	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["result"] != true {
		t.Errorf("expected Promise to be defined, got %v", resultMap["result"])
	}
}

// TestModernJSSymbols verifies Symbol support
func TestModernJSSymbols(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		const sym = Symbol('test');
		({ result: typeof sym === 'symbol', description: sym.toString() })
	`
	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["result"] != true {
		t.Errorf("expected typeof Symbol to be 'symbol', got %v", resultMap["result"])
	}
}

// TestModernJSMapSet verifies Map and Set work
func TestModernJSMapSet(t *testing.T) {
	caller := newMockToolCaller()

	tests := []struct {
		name string
		code string
	}{
		{
			"Map basics",
			`
				const m = new Map();
				m.set('key1', 'value1');
				m.set('key2', 'value2');
				({ size: m.size, hasKey1: m.has('key1'), value: m.get('key1') })
			`,
		},
		{
			"Set basics",
			`
				const s = new Set([1, 2, 3, 2, 1]);
				({ size: s.size, has2: s.has(2), has4: s.has(4) })
			`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Execute(context.Background(), caller, tt.code, ExecutionOptions{})
			if !result.Ok {
				t.Fatalf("expected ok=true, got error: %v", result.Error)
			}
		})
	}
}

// TestModernJSOptionalChaining verifies optional chaining (?.) works
func TestModernJSOptionalChaining(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		const obj = { user: { name: "Alice" } };
		const missing = { user: null };
		({
			found: obj.user?.name,
			notFound: missing.user?.name ?? "default",
			deepMissing: obj.foo?.bar?.baz ?? "fallback"
		})
	`
	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["found"] != "Alice" {
		t.Errorf("expected 'Alice', got %v", resultMap["found"])
	}
	if resultMap["notFound"] != "default" {
		t.Errorf("expected 'default', got %v", resultMap["notFound"])
	}
	if resultMap["deepMissing"] != "fallback" {
		t.Errorf("expected 'fallback', got %v", resultMap["deepMissing"])
	}
}

// TestModernJSNullishCoalescing verifies nullish coalescing (??) works
func TestModernJSNullishCoalescing(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		const a = null ?? "default_a";
		const b = undefined ?? "default_b";
		const c = 0 ?? "default_c";
		const d = "" ?? "default_d";
		({ a, b, c, d })
	`
	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["a"] != "default_a" {
		t.Errorf("expected 'default_a', got %v", resultMap["a"])
	}
	if resultMap["b"] != "default_b" {
		t.Errorf("expected 'default_b', got %v", resultMap["b"])
	}
	// 0 is not null/undefined, so ?? should return 0
	if toInt64(resultMap["c"]) != 0 {
		t.Errorf("expected 0, got %v", resultMap["c"])
	}
	// "" is not null/undefined, so ?? should return ""
	if resultMap["d"] != "" {
		t.Errorf("expected '', got %v", resultMap["d"])
	}
}

// TestModernJSGenerators verifies generator function support
func TestModernJSGenerators(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		function* range(start, end) {
			for (let i = start; i < end; i++) {
				yield i;
			}
		}

		const nums = [];
		for (const n of range(1, 5)) {
			nums.push(n);
		}
		({ result: nums })
	`
	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	nums := resultMap["result"].([]interface{})
	if len(nums) != 4 {
		t.Errorf("expected 4 numbers, got %d", len(nums))
	}
}

// TestModernJSProxyReflect verifies Proxy and Reflect support
func TestModernJSProxyReflect(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		const handler = {
			get: function(target, prop) {
				return prop in target ? target[prop] : "default";
			}
		};

		const obj = new Proxy({ name: "Alice" }, handler);
		({
			name: obj.name,
			missing: obj.missing,
			hasReflect: typeof Reflect !== 'undefined'
		})
	`
	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["name"] != "Alice" {
		t.Errorf("expected 'Alice', got %v", resultMap["name"])
	}
	if resultMap["missing"] != "default" {
		t.Errorf("expected 'default', got %v", resultMap["missing"])
	}
	if resultMap["hasReflect"] != true {
		t.Errorf("expected Reflect to be defined")
	}
}

// TestModernJSComputedPropertyNames verifies computed property names work
func TestModernJSComputedPropertyNames(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		const key = "dynamic";
		const obj = { [key]: "value", ["computed_" + 1]: true };
		({ result: obj.dynamic, computed: obj.computed_1 })
	`
	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["result"] != "value" {
		t.Errorf("expected 'value', got %v", resultMap["result"])
	}
}

// TestModernJSShorthandProperties verifies shorthand property syntax works
func TestModernJSShorthandProperties(t *testing.T) {
	caller := newMockToolCaller()

	code := `
		const name = "Alice";
		const age = 30;
		const obj = { name, age };
		({ result: obj.name + " is " + obj.age })
	`
	result := Execute(context.Background(), caller, code, ExecutionOptions{})
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["result"] != "Alice is 30" {
		t.Errorf("expected 'Alice is 30', got %v", resultMap["result"])
	}
}

// TestModernJSWithToolCalls verifies modern JS syntax works with tool calling
func TestModernJSWithToolCalls(t *testing.T) {
	caller := newMockToolCaller()
	caller.results["github:get_user"] = map[string]interface{}{
		"login": "octocat",
		"id":    583231,
		"name":  "The Octocat",
	}
	caller.results["github:list_repos"] = []interface{}{
		map[string]interface{}{"name": "repo1", "stars": 100},
		map[string]interface{}{"name": "repo2", "stars": 200},
	}

	code := `
		const userRes = call_tool("github", "get_user", { username: input.username });
		if (!userRes.ok) throw new Error("Failed: " + userRes.error.message);

		const reposRes = call_tool("github", "list_repos", { user: input.username });
		if (!reposRes.ok) throw new Error("Failed: " + reposRes.error.message);

		const repos = reposRes.result.map(r => ({
			name: r.name,
			stars: r.stars
		}));

		const totalStars = repos.reduce((sum, r) => sum + r.stars, 0);

		({
			user: userRes.result.name,
			repos,
			totalStars,
			summary: ` + "`${userRes.result.name} has ${repos.length} repos with ${totalStars} stars`" + `
		})
	`
	opts := ExecutionOptions{
		Input: map[string]interface{}{
			"username": "octocat",
		},
	}

	result := Execute(context.Background(), caller, code, opts)
	if !result.Ok {
		t.Fatalf("expected ok=true, got error: %v", result.Error)
	}

	resultMap := result.Value.(map[string]interface{})
	if resultMap["user"] != "The Octocat" {
		t.Errorf("expected 'The Octocat', got %v", resultMap["user"])
	}
	if toInt64(resultMap["totalStars"]) != 300 {
		t.Errorf("expected totalStars=300, got %v", resultMap["totalStars"])
	}
	expectedSummary := "The Octocat has 2 repos with 300 stars"
	if resultMap["summary"] != expectedSummary {
		t.Errorf("expected summary=%q, got %v", expectedSummary, resultMap["summary"])
	}
}

// toInt64 converts a numeric interface{} to int64
func toInt64(v interface{}) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case float64:
		return int64(val)
	case int:
		return int64(val)
	default:
		return 0
	}
}
