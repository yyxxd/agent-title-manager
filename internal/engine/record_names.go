package engine

import "context"

type RecordIdentity struct {
	Agent, OrcaID, Kind string
}

type RecordName struct {
	Kind, Name string
}

type RecordNameReader func(context.Context, []RecordIdentity) map[string]RecordName

func AddRecordNames(ctx context.Context, result any, read RecordNameReader) any {
	list, ok := result.(recordList)
	if !ok || read == nil {
		return result
	}
	keys := make([]RecordIdentity, 0, len(list.Items))
	for _, item := range list.Items {
		if item.orcaID != "" {
			keys = append(keys, RecordIdentity{item.Agent, item.orcaID, item.Kind})
		}
	}
	if len(keys) == 0 {
		return result
	}
	names := read(ctx, keys)
	for j := range list.Items {
		item := &list.Items[j]
		if label, ok := names[item.orcaID]; ok {
			item.Kind, item.IdentityName = label.Kind, label.Name
		}
	}
	return list
}
