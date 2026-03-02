package graph

import (
	"context"
	"fmt"
	"time"

	"github.com/wricardo/pathwalk/examples/pizzeria-server/graph/model"
)

// --- Query resolvers ---

func (r *queryResolver) Pizzas(ctx context.Context) ([]*model.Pizza, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]*model.Pizza, 0, len(r.pizzas))
	for _, p := range r.pizzas {
		list = append(list, copyPizza(p))
	}
	return list, nil
}

func (r *queryResolver) Pizza(ctx context.Context, id string) (*model.Pizza, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.pizzas[id]
	if !ok {
		return nil, fmt.Errorf("pizza %q not found", id)
	}
	return copyPizza(p), nil
}

func (r *queryResolver) Orders(ctx context.Context) ([]*model.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]*model.Order, 0, len(r.orders))
	for _, o := range r.orders {
		list = append(list, copyOrder(o))
	}
	return list, nil
}

func (r *queryResolver) Order(ctx context.Context, id string) (*model.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.orders[id]
	if !ok {
		return nil, fmt.Errorf("order %q not found", id)
	}
	return copyOrder(o), nil
}

func (r *queryResolver) Inventory(ctx context.Context) ([]*model.Ingredient, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]*model.Ingredient, 0, len(r.ingredients))
	for _, i := range r.ingredients {
		list = append(list, copyIngredient(i))
	}
	return list, nil
}

func (r *queryResolver) Ingredient(ctx context.Context, id string) (*model.Ingredient, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ing, ok := r.ingredients[id]
	if !ok {
		return nil, fmt.Errorf("ingredient %q not found", id)
	}
	return copyIngredient(ing), nil
}

func (r *queryResolver) Dashboard(ctx context.Context) (*model.Dashboard, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	today := time.Now().Format("2006-01-02")
	var completedToday int
	var totalRevenue, totalCost float64

	for _, o := range r.orders {
		totalRevenue += o.Revenue
		totalCost += o.Cost
		if o.Status == model.OrderStatusCompleted {
			if createdAt, ok := r.orderCreatedAt[o.ID]; ok && createdAt.Format("2006-01-02") == today {
				completedToday++
			}
		}
	}

	var lowInventoryCount int
	for _, ing := range r.ingredients {
		if float64(ing.Current) < float64(ing.Max)*0.25 {
			lowInventoryCount++
		}
	}

	return &model.Dashboard{
		TotalOrders:       len(r.orders),
		CompletedToday:    completedToday,
		LowInventoryCount: lowInventoryCount,
		TotalRevenue:      totalRevenue,
		BottomLine:        totalRevenue - totalCost,
	}, nil
}

// --- Mutation resolvers ---

func (r *mutationResolver) CreatePizza(ctx context.Context, input model.CreatePizzaInput) (*model.Pizza, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, ing := range input.Ingredients {
		if _, ok := r.ingredients[ing.IngredientID]; !ok {
			return nil, fmt.Errorf("ingredient %q not found", ing.IngredientID)
		}
	}

	ingredients := make([]*model.PizzaIngredient, len(input.Ingredients))
	for i, ing := range input.Ingredients {
		ingredients[i] = &model.PizzaIngredient{
			IngredientID: ing.IngredientID,
			Quantity:     ing.Quantity,
		}
	}

	p := &model.Pizza{
		ID:          r.genID(),
		Name:        input.Name,
		Price:       input.Price,
		Ingredients: ingredients,
	}
	r.pizzas[p.ID] = p
	return copyPizza(p), nil
}

func (r *mutationResolver) DeletePizza(ctx context.Context, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pizzas[id]; !ok {
		return false, fmt.Errorf("pizza %q not found", id)
	}
	delete(r.pizzas, id)
	return true, nil
}

func (r *mutationResolver) CreateOrder(ctx context.Context, input model.CreateOrderInput) (*model.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var revenue, cost float64
	items := make([]*model.OrderItem, len(input.Items))
	for i, item := range input.Items {
		pizza, ok := r.pizzas[item.PizzaID]
		if !ok {
			return nil, fmt.Errorf("pizza %q not found", item.PizzaID)
		}
		items[i] = &model.OrderItem{PizzaID: item.PizzaID, Quantity: item.Quantity}
		revenue += pizza.Price * float64(item.Quantity)
		cost += r.pizzaCost(pizza) * float64(item.Quantity)
	}

	o := &model.Order{
		ID:       r.genID(),
		Customer: input.Customer,
		Status:   model.OrderStatusPending,
		Revenue:  revenue,
		Cost:     cost,
		Items:    items,
	}
	r.orders[o.ID] = o
	r.orderCreatedAt[o.ID] = time.Now()
	return copyOrder(o), nil
}

func (r *mutationResolver) UpdateOrderStatus(ctx context.Context, id string, status model.OrderStatus) (*model.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.orders[id]
	if !ok {
		return nil, fmt.Errorf("order %q not found", id)
	}
	o.Status = status
	return copyOrder(o), nil
}

func (r *mutationResolver) DeleteOrder(ctx context.Context, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.orders[id]; !ok {
		return false, fmt.Errorf("order %q not found", id)
	}
	delete(r.orders, id)
	delete(r.orderCreatedAt, id)
	return true, nil
}

func (r *mutationResolver) RestockIngredient(ctx context.Context, id string, amount int) (*model.Ingredient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ing, ok := r.ingredients[id]
	if !ok {
		return nil, fmt.Errorf("ingredient %q not found", id)
	}
	ing.Current += amount
	if ing.Current > ing.Max {
		ing.Current = ing.Max
	}
	if ing.Current < 0 {
		ing.Current = 0
	}
	return copyIngredient(ing), nil
}

func (r *mutationResolver) UpdateIngredient(ctx context.Context, id string, current int) (*model.Ingredient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ing, ok := r.ingredients[id]
	if !ok {
		return nil, fmt.Errorf("ingredient %q not found", id)
	}
	ing.Current = current
	return copyIngredient(ing), nil
}

// --- Resolver sub-types ---

type queryResolver struct{ *Resolver }
type mutationResolver struct{ *Resolver }

func (r *Resolver) Query() QueryResolver       { return &queryResolver{r} }
func (r *Resolver) Mutation() MutationResolver { return &mutationResolver{r} }
