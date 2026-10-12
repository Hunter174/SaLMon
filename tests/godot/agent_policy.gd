extends RefCounted
# Example GAME-side policy contract. This is not part of SaLMon's inference runtime.
# facts are authoritative: booleans, inventory and already-committed reservations.
# Every relevant fact change must increment the agent's revision before applying results.

static func filter_actions(facts: Dictionary, actions: Array, mandatory_ids: Array = []) -> Dictionary:
    if not facts.has("inventory") or not facts.has("reserved") or not facts.inventory is Dictionary or not facts.reserved is Dictionary:
        return {"error": "inventory and reserved dictionaries are required"}
    var seen := {}
    var options := []
    for action in actions:
        if not action is Dictionary or not action.has("id") or not action.has("description") or not action.has("enabled") or not action.has("requires") or not action.has("costs"):
            return {"error": "incomplete action contract"}
        if not action.id is String or action.id.is_empty() or seen.has(action.id) or not action.description is String or action.description.is_empty() or not action.enabled is bool or not action.requires is Dictionary or not action.costs is Dictionary:
            return {"error": "invalid or duplicate action"}
        seen[action.id] = true
        if not action.enabled or (not mandatory_ids.is_empty() and not mandatory_ids.has(action.id)):
            continue
        var allowed := true
        for key in action.requires:
            # Missing facts NEVER satisfy a requirement, including false requirements.
            if not facts.has(key) or facts[key] != action.requires[key]:
                allowed = false
                break
        if not allowed:
            continue
        for resource in action.costs:
            var amount = action.costs[resource]
            if not amount is int or amount < 0 or not facts.inventory.has(resource) or not facts.reserved.has(resource) or not facts.inventory[resource] is int or not facts.reserved[resource] is int or facts.reserved[resource] < 0 or facts.inventory[resource] - facts.reserved[resource] < amount:
                allowed = false
                break
        if allowed:
            options.append({"id": action.id, "description": action.description})
    for id in mandatory_ids:
        if not seen.has(id):
            return {"error": "mandatory action missing from contract"}
    return {"options": options}
