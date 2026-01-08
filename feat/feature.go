package feat

import (
	"fmt"
	"strings"

	"github.com/antchfx/xmlquery"
	"github.com/bbredesen/vk-gen/def"
)

type Feature struct {
	apiName, featureName string
	version              string

	requireTypeNames, requireValueNames map[string]bool
	ResolvedTypes                       def.TypeRegistry
	ResolvedValues                      map[string]def.ValueRegistry
}

func NewFeature() *Feature {
	return &Feature{
		requireTypeNames:  make(map[string]bool),
		requireValueNames: make(map[string]bool),
		ResolvedTypes:     make(def.TypeRegistry),
		ResolvedValues:    make(map[string]def.ValueRegistry),
	}

}

func (f *Feature) MergeIncludeSet(is *def.IncludeSet) {
	for k := range is.IncludeTypes {
		f.requireTypeNames[k] = true
	}
	for k := range is.IncludeValues {
		f.requireValueNames[k] = true
	}

	for k, v := range is.ResolvedTypes {
		f.ResolvedTypes[k] = v
	}
	for k, v := range is.ResolvedValues {
		// var useTypeName string = "!none"
		// if v.ResolvedType() != nil {
		useTypeName := v.UnderlyingTypeName()
		// }

		if _, found := f.ResolvedValues[useTypeName]; !found {
			f.ResolvedValues[useTypeName] = make(def.ValueRegistry)
		}

		f.ResolvedValues[useTypeName][k] = v
	}

}

func (f *Feature) Resolve(tr def.TypeRegistry, vr def.ValueRegistry) {
	for k := range f.requireTypeNames {
		if tr[k] == nil {
			continue // Skip types not found in registry
		}
		f.MergeIncludeSet(tr[k].Resolve(tr, vr))
	}

	for k, v := range vr {
		if v.IsCore() && f.ResolvedTypes[vr[k].UnderlyingTypeName()] != nil {
			f.requireValueNames[k] = true
		}
	}

	for k := range f.requireValueNames {
		val := vr[k]
		f.MergeIncludeSet(val.Resolve(tr, vr))

		resVals, found := f.ResolvedValues[val.UnderlyingTypeName()]
		if !found {
			f.ResolvedValues[val.UnderlyingTypeName()] = make(def.ValueRegistry)
			resVals = f.ResolvedValues[val.UnderlyingTypeName()]
		}
		resVals[val.RegistryName()] = val
	}
}

func (f *Feature) FilterByCategory() map[def.TypeCategory]*Feature {
	rval := make(map[def.TypeCategory]*Feature)

	for _, t := range f.ResolvedTypes {
		inc := rval[t.Category()]
		if inc == nil {
			inc = NewFeature()
			rval[t.Category()] = inc
		}

		inc.ResolvedTypes[t.RegistryName()] = t
	}

	// Stuff all the values, segmented first by category then by type, into the new Feature
	// Lots of maps to make...
	for k, vr := range f.ResolvedValues {
		_ = k
		// Default category reset before starting the inner loop
		cat := def.CatNone

		for valName, valDef := range vr {
			if valDef.ResolvedType() != nil {
				cat = valDef.ResolvedType().Category()
			} else {
				cat = def.CatExten
			}

			_, found := rval[cat]
			if !found {
				rval[cat] = NewFeature()
			}

			m := rval[cat].ResolvedValues[valDef.UnderlyingTypeName()]
			if m == nil {
				m = make(def.ValueRegistry)
				rval[cat].ResolvedValues[valDef.UnderlyingTypeName()] = m
			}

			m[valName] = valDef
		}
	}

	return rval
}

func ReadFeatureFromXML(featureNode *xmlquery.Node, tr def.TypeRegistry, vr def.ValueRegistry) *Feature {
	if featureNode == nil {
		return nil
	}

	// Find the root document by traversing up from featureNode
	root := featureNode
	for root.Parent != nil {
		root = root.Parent
	}

	visited := make(map[string]bool)
	return readFeatureFromXMLWithDeps(featureNode, root, tr, vr, visited)
}

func readFeatureFromXMLWithDeps(featureNode, root *xmlquery.Node, tr def.TypeRegistry, vr def.ValueRegistry, visited map[string]bool) *Feature {
	if featureNode == nil {
		return nil
	}

	featureName := featureNode.SelectAttr("name")

	// Avoid infinite loops from circular dependencies
	if visited[featureName] {
		return nil
	}
	visited[featureName] = true

	rval := NewFeature()
	rval.apiName = featureNode.SelectAttr("api")
	rval.featureName = featureName
	rval.version = featureNode.SelectAttr("number")

	// Process the "depends" attribute - this is crucial for Vulkan 1.4+
	// Dependencies can be comma-separated, e.g., "VK_VERSION_1_0,VK_GRAPHICS_VERSION_1_1"
	depends := featureNode.SelectAttr("depends")
	if depends != "" {
		depNames := strings.Split(depends, ",")
		for _, depName := range depNames {
			depName = strings.TrimSpace(depName)
			if depName == "" {
				continue
			}

			// Find the dependent feature node
			xpath := fmt.Sprintf("//feature[@name='%s']", depName)
			depNode := xmlquery.FindOne(root, xpath)
			if depNode != nil {
				depFeature := readFeatureFromXMLWithDeps(depNode, root, tr, vr, visited)
				if depFeature != nil {
					rval.MergeWith(depFeature)
				}
			}
		}
	}

	for _, reqNode := range xmlquery.Find(featureNode, "/require") {
		for _, typeNode := range xmlquery.Find(reqNode, "/type") {
			rval.requireTypeNames[typeNode.SelectAttr("name")] = true
		}

		for _, cmdNode := range xmlquery.Find(reqNode, "/command") {
			rval.requireTypeNames[cmdNode.SelectAttr("name")] = true
		}

		for _, enumNode := range xmlquery.Find(reqNode, "/enum") {
			extendsTypeName := enumNode.SelectAttr("extends")

			if extendsTypeName != "" {
				// Defines a new enum value, which extends a global type
				td := tr[extendsTypeName]
				if enumNode.SelectAttr("bitpos") != "" {
					vd := def.NewBitmaskValueFromXML(td, enumNode)
					vr[vd.RegistryName()] = vd
				} else {
					vd := def.NewEnumValueFromXML(td, enumNode)
					vr[vd.RegistryName()] = vd
				}
			}

			rval.requireValueNames[enumNode.SelectAttr("name")] = true
		}
	}

	return rval
}

func (f *Feature) Name() string { return f.featureName }

func (f *Feature) MergeWith(g *Feature) {
	if g == nil {
		return
	}
	for k, v := range g.requireTypeNames {
		f.requireTypeNames[k] = v
	}
	for k, v := range g.requireValueNames {
		f.requireValueNames[k] = v
	}
}
