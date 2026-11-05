package protocol

import "fmt"

// Constraint calls share immutable checks only within the same static scope.
// Dependencies follow cached calls so document fingerprints include every rule.
type wireConstraintCompiler struct {
	definitions map[string][]WireAssertion
	references  map[string][]WireAssertion
	used        map[string]bool
	resolving   map[string]bool
	instances   map[referenceContext][]compiledWireConstraint
}

type compiledWireConstraint struct {
	scope        compiledReference
	checks       []compiledWireAssertion
	dependencies []string
}

func newWireConstraintCompiler(definitions map[string][]WireAssertion) *wireConstraintCompiler {
	return &wireConstraintCompiler{definitions: definitions, used: map[string]bool{},
		resolving: map[string]bool{}, instances: map[referenceContext][]compiledWireConstraint{}}
}

func (expressions *expressionCompiler) compileWireConstraint(assertion WireAssertion, path string, scope expressionScope, depth int) ([]compiledWireAssertion, error) {
	if assertion.When != nil || assertion.Schema != nil || assertion.Code != "" ||
		assertion.Reason != "" || assertion.Check != nil || assertion.Each != nil || assertion.ReasonExpression != nil || assertion.Diagnostic != nil {
		return nil, fmt.Errorf("%s/ref: a constraint call cannot override its declaration", path)
	}
	compiler, name := expressions.constraints, assertion.Ref
	if compiler == nil || !definitionIdentifier.MatchString(name) || len(compiler.definitions[name]) == 0 || compiler.resolving[name] {
		return nil, fmt.Errorf("%s/ref: requires a nonempty, nonrecursive Spec constraint %q", path, name)
	}
	compiler.used[name] = true
	key := referenceKey(name, scope)
	for _, cached := range compiler.instances[key] {
		if !cached.scope.matches(scope) {
			continue
		}
		maximumDepth := depth + cached.scope.expansionDepth
		if maximumDepth > expressions.limits.Depth {
			return nil, fmt.Errorf("%s/ref: constraint call exceeds the depth limit", path)
		}
		expressions.maximumDepth = max(expressions.maximumDepth, maximumDepth)
		expressions.includeReferences(cached.scope.dependencies)
		compiler.include(cached.dependencies)
		return cached.checks, nil
	}

	parentExpressions, parentConstraints := expressions.mappingReferences, compiler.references
	expressionDependencies := map[string]Expression{}
	constraintDependencies := map[string][]WireAssertion{name: compiler.definitions[name]}
	expressions.mappingReferences, compiler.references = expressionDependencies, constraintDependencies
	compiler.resolving[name] = true
	parentDepth := expressions.maximumDepth
	expressions.maximumDepth = depth
	checks, err := compileWireAssertionsInScope(compiler.definitions[name], "/spec/constraints/"+escapePointer(name), scope, expressions, depth+1)
	expansionDepth := expressions.maximumDepth - depth
	expressions.maximumDepth = max(parentDepth, expressions.maximumDepth)
	delete(compiler.resolving, name)
	expressions.mappingReferences, compiler.references = parentExpressions, parentConstraints
	if err != nil {
		return nil, err
	}
	context := captureReferenceScope(scope)
	context.expansionDepth, context.dependencies = expansionDepth, sortedKeys(expressionDependencies)
	compiled := compiledWireConstraint{scope: context, checks: checks, dependencies: sortedKeys(constraintDependencies)}
	compiler.instances[key] = append(compiler.instances[key], compiled)
	expressions.includeReferences(compiled.scope.dependencies)
	compiler.include(compiled.dependencies)
	return checks, nil
}

func (compiler *wireConstraintCompiler) include(names []string) {
	if compiler.references != nil {
		for _, name := range names {
			compiler.references[name] = compiler.definitions[name]
		}
	}
}
