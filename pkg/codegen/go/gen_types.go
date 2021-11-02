package gen

import (
	"github.com/pulumi/pulumi/pkg/v3/codegen"
	"github.com/pulumi/pulumi/pkg/v3/codegen/schema"
)

type outputType struct {
	schema.Type

	elementType schema.Type
}

type typeDetails struct {
	optionalInputType bool

	inputTypes  []*schema.InputType
	outputTypes []*outputType
}

type genContext struct {
	tool          string
	pulumiPackage *schema.Package
	info          GoPackageInfo

	goPackages    map[string]*pkgContext
	resourceTypes map[string]*schema.ResourceType
	inputTypes    map[schema.Type]*schema.InputType
	outputTypes   map[schema.Type]*outputType

	notedTypes codegen.Set
}

func newGenContext(tool string, pulumiPackage *schema.Package, info GoPackageInfo) *genContext {
	return &genContext{
		tool:          tool,
		pulumiPackage: pulumiPackage,
		info:          info,
		goPackages:    map[string]*pkgContext{},
		resourceTypes: map[string]*schema.ResourceType{},
		inputTypes:    map[schema.Type]*schema.InputType{},
		outputTypes:   map[schema.Type]*outputType{},
		notedTypes:    codegen.Set{},
	}
}

func (ctx *genContext) getPackageForModule(mod string) *pkgContext {
	p, ok := ctx.goPackages[mod]
	if !ok {
		p = &pkgContext{
			ctx:                           ctx,
			pkg:                           ctx.pulumiPackage,
			mod:                           mod,
			importBasePath:                ctx.info.ImportBasePath,
			rootPackageName:               ctx.info.RootPackageName,
			typeDetails:                   map[schema.Type]*typeDetails{},
			names:                         codegen.NewStringSet(),
			schemaNames:                   codegen.NewStringSet(),
			renamed:                       map[string]string{},
			duplicateTokens:               map[string]bool{},
			functionNames:                 map[*schema.Function]string{},
			tool:                          ctx.tool,
			modToPkg:                      ctx.info.ModuleToPackage,
			pkgImportAliases:              ctx.info.PackageImportAliases,
			packages:                      ctx.goPackages,
			liftSingleValueMethodReturns:  ctx.info.LiftSingleValueMethodReturns,
			disableInputTypeRegistrations: ctx.info.DisableInputTypeRegistrations,
		}
		ctx.goPackages[mod] = p
	}
	return p
}

func (ctx *genContext) getPackageForToken(token string) *pkgContext {
	return ctx.getPackageForModule(tokenToPackage(ctx.pulumiPackage, ctx.info.ModuleToPackage, token))
}

func (ctx *genContext) getPackageForType(t schema.Type) *pkgContext {
	_, pkg := ctx.getRepresentativeTypeAndPackage(t)
	return pkg
}

func (ctx *genContext) getRepresentativeTypeAndPackage(t schema.Type) (schema.Type, *pkgContext) {
	switch t := t.(type) {
	case *outputType:
		return ctx.getRepresentativeTypeAndPackage(t.elementType)
	case *schema.InputType:
		return ctx.getRepresentativeTypeAndPackage(t.ElementType)
	case *schema.OptionalType:
		return ctx.getRepresentativeTypeAndPackage(t.ElementType)
	case *schema.ArrayType:
		return ctx.getRepresentativeTypeAndPackage(t.ElementType)
	case *schema.MapType:
		return ctx.getRepresentativeTypeAndPackage(t.ElementType)
	case *schema.ObjectType:
		return t, ctx.getPackageForToken(t.Token)
	case *schema.EnumType:
		return t, ctx.getPackageForToken(t.Token)
	case *schema.ResourceType:
		return t, ctx.getPackageForToken(t.Token)
	case *schema.TokenType:
		return t, ctx.getPackageForToken(t.Token)
	default:
		return nil, nil
	}
}

func (ctx *genContext) inputType(elementType schema.Type) *schema.InputType {
	t, ok := ctx.inputTypes[elementType]
	if !ok {
		t = &schema.InputType{ElementType: elementType}
		ctx.inputTypes[elementType] = t
	}
	return t
}

func (ctx *genContext) outputType(elementType schema.Type) *outputType {
	t, ok := ctx.outputTypes[elementType]
	if !ok {
		t = &outputType{elementType: elementType}
		ctx.outputTypes[elementType] = t
	}
	return t
}

func (ctx *genContext) resourceType(resource *schema.Resource) *schema.ResourceType {
	t, ok := ctx.resourceTypes[resource.Token]
	if !ok {
		t = &schema.ResourceType{
			Token:    resource.Token,
			Resource: resource,
		}
		ctx.resourceTypes[resource.Token] = t
	}
	return t
}

func (ctx *genContext) noteType(t schema.Type) {
	if ctx.notedTypes.Has(t) {
		return
	}

	ctx.notedTypes.Add(t)
	switch t := t.(type) {
	case *outputType:
		ctx.noteOutputType(t)
	case *schema.InputType:
		ctx.noteInputType(t)
	case *schema.OptionalType:
		ctx.noteOptionalType(t)
	case *schema.ArrayType:
		ctx.noteType(t.ElementType)
	case *schema.MapType:
		ctx.noteType(t.ElementType)
	case *schema.UnionType:
		ctx.noteUnionType(t)
	case *schema.ObjectType:
		ctx.noteObjectType(t)
	case *schema.EnumType:
		pkg := ctx.getPackageForType(t)
		pkg.enums = append(pkg.enums, t)
	}
}

func (ctx *genContext) noteOutputType(t *outputType) {
	// For optional, array, map, and object output types, we need to note some additional
	// output types. In the former three cases, we need to note the output of the type's
	// element type so we can generate element accessors. In the latter case, we need to
	// note an output type per property type so we can generate property accessors.
	switch t := t.elementType.(type) {
	case *schema.OptionalType:
		ctx.noteType(ctx.outputType(t.ElementType))
	case *schema.ArrayType:
		ctx.noteType(ctx.outputType(t.ElementType))
	case *schema.MapType:
		ctx.noteType(ctx.outputType(t.ElementType))
	case *schema.ObjectType:
		for _, p := range t.Properties {
			ctx.noteType(ctx.outputType(p.Type))
		}
	}

	if representativeType, pkg := ctx.getRepresentativeTypeAndPackage(t); pkg != nil {
		details := pkg.detailsForType(representativeType)
		details.outputTypes = append(details.outputTypes, t)
	}
}

func (ctx *genContext) noteInputType(t *schema.InputType) {
	ctx.noteType(t.ElementType)
	ctx.noteOutputType(ctx.outputType(codegen.ResolvedType(t.ElementType)))
	if representativeType, pkg := ctx.getRepresentativeTypeAndPackage(t); pkg != nil {
		details := pkg.detailsForType(representativeType)
		details.inputTypes = append(details.inputTypes, t)
	}
}

func (ctx *genContext) noteOptionalType(t *schema.OptionalType) {
	// Go generates optional inputs as inputs of optionals.
	if input, ok := t.ElementType.(*schema.InputType); ok {
		ctx.noteInputType(&schema.InputType{
			ElementType: &schema.OptionalType{
				ElementType: input.ElementType,
			},
		})

		pkg := ctx.getPackageForType(input.ElementType)
		pkg.detailsForType(input.ElementType).optionalInputType = true

		return
	}

	ctx.noteType(t.ElementType)
}

func (ctx *genContext) noteUnionType(t *schema.UnionType) {
	for _, t := range t.ElementTypes {
		ctx.noteType(t)
	}
}

func (ctx *genContext) noteObjectType(t *schema.ObjectType) {
	ctx.notePropertyTypes(t.Properties)
}

func (ctx *genContext) notePropertyTypes(props []*schema.Property) {
	for _, p := range props {
		ctx.noteType(p.Type)
	}
}

func (ctx *genContext) noteOutputPropertyTypes(props []*schema.Property) {
	for _, p := range props {
		ctx.noteType(ctx.outputType(p.Type))
	}
}
