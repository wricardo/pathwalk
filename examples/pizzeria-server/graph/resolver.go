package graph

import (
	"fmt"
	"sync"
	"time"

	"github.com/wricardo/pathwalk/examples/pizzeria-server/graph/model"
)

// Resolver is the root resolver. It holds the in-memory store.
type Resolver struct {
	mu             sync.RWMutex
	pizzas         map[string]*model.Pizza
	orders         map[string]*model.Order
	ingredients    map[string]*model.Ingredient
	orderCreatedAt map[string]time.Time
	nextID         int
}

func NewResolver() *Resolver {
	r := &Resolver{
		pizzas:         make(map[string]*model.Pizza),
		orders:         make(map[string]*model.Order),
		ingredients:    make(map[string]*model.Ingredient),
		orderCreatedAt: make(map[string]time.Time),
		nextID:         1,
	}
	r.seed()
	return r
}

func (r *Resolver) genID() string {
	id := r.nextID
	r.nextID++
	return fmt.Sprintf("%d", id)
}

// seed populates the store with fixed-ID sample data so IDs are predictable.
func (r *Resolver) seed() {
	// Ingredients — fixed IDs: ing-1 … ing-6
	flour := &model.Ingredient{ID: "ing-1", Name: "Flour", Unit: "kg", Current: 50, Max: 100, CostPerUnit: 1.20}
	tomato := &model.Ingredient{ID: "ing-2", Name: "Tomato Sauce", Unit: "liters", Current: 20, Max: 40, CostPerUnit: 2.50}
	mozzarella := &model.Ingredient{ID: "ing-3", Name: "Mozzarella", Unit: "kg", Current: 15, Max: 30, CostPerUnit: 8.00}
	basil := &model.Ingredient{ID: "ing-4", Name: "Fresh Basil", Unit: "bunches", Current: 3, Max: 20, CostPerUnit: 1.00}
	pepperoni := &model.Ingredient{ID: "ing-5", Name: "Pepperoni", Unit: "kg", Current: 8, Max: 20, CostPerUnit: 12.00}
	mushrooms := &model.Ingredient{ID: "ing-6", Name: "Mushrooms", Unit: "kg", Current: 2, Max: 15, CostPerUnit: 4.00}
	for _, ing := range []*model.Ingredient{flour, tomato, mozzarella, basil, pepperoni, mushrooms} {
		r.ingredients[ing.ID] = ing
	}

	// Pizzas — fixed IDs: pizza-1 … pizza-3
	margherita := &model.Pizza{
		ID:    "pizza-1",
		Name:  "Margherita",
		Price: 10.99,
		Ingredients: []*model.PizzaIngredient{
			{IngredientID: flour.ID, Quantity: 1},
			{IngredientID: tomato.ID, Quantity: 1},
			{IngredientID: mozzarella.ID, Quantity: 1},
			{IngredientID: basil.ID, Quantity: 1},
		},
	}
	pepperoniPizza := &model.Pizza{
		ID:    "pizza-2",
		Name:  "Pepperoni",
		Price: 13.99,
		Ingredients: []*model.PizzaIngredient{
			{IngredientID: flour.ID, Quantity: 1},
			{IngredientID: tomato.ID, Quantity: 1},
			{IngredientID: mozzarella.ID, Quantity: 1},
			{IngredientID: pepperoni.ID, Quantity: 1},
		},
	}
	funghiPizza := &model.Pizza{
		ID:    "pizza-3",
		Name:  "Funghi",
		Price: 12.49,
		Ingredients: []*model.PizzaIngredient{
			{IngredientID: flour.ID, Quantity: 1},
			{IngredientID: tomato.ID, Quantity: 1},
			{IngredientID: mozzarella.ID, Quantity: 1},
			{IngredientID: mushrooms.ID, Quantity: 2},
		},
	}
	for _, p := range []*model.Pizza{margherita, pepperoniPizza, funghiPizza} {
		r.pizzas[p.ID] = p
	}
}

// pizzaCost computes ingredient cost for a pizza (caller must hold at least r.mu.RLock).
func (r *Resolver) pizzaCost(p *model.Pizza) float64 {
	var cost float64
	for _, ing := range p.Ingredients {
		if ingredient, ok := r.ingredients[ing.IngredientID]; ok {
			cost += ingredient.CostPerUnit * float64(ing.Quantity)
		}
	}
	return cost
}

// copy helpers — return snapshots so gqlgen reads stable values after the lock is released.

func copyIngredient(i *model.Ingredient) *model.Ingredient {
	cp := *i
	return &cp
}

func copyOrder(o *model.Order) *model.Order {
	cp := *o
	cp.Items = make([]*model.OrderItem, len(o.Items))
	copy(cp.Items, o.Items)
	return &cp
}

func copyPizza(p *model.Pizza) *model.Pizza {
	cp := *p
	cp.Ingredients = make([]*model.PizzaIngredient, len(p.Ingredients))
	copy(cp.Ingredients, p.Ingredients)
	return &cp
}
