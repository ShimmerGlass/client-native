package configuration

import (
	"fmt"
	"strconv"
	"strings"

	strfmt "github.com/go-openapi/strfmt"
	parser "github.com/haproxytech/config-parser"
	parser_errors "github.com/haproxytech/config-parser/errors"
	"github.com/haproxytech/config-parser/params"
	"github.com/haproxytech/config-parser/types"
	"github.com/haproxytech/models"
)

// GetBinds returns a struct with configuration version and an array of
// configured binds in the specified frontend. Returns error on fail.
func (c *Client) GetBinds(frontend string, transactionID string) (*models.GetBindsOKBody, error) {
	if c.Cache.Enabled() {
		binds, found := c.Cache.Binds.Get(frontend, transactionID)
		if found {
			return &models.GetBindsOKBody{Version: c.Cache.Version.Get(transactionID), Data: binds}, nil
		}
	}
	if err := c.ConfigParser.LoadData(c.getTransactionFile(transactionID)); err != nil {
		return nil, err
	}

	binds, err := c.parseBinds(frontend)
	if err != nil {
		if err == parser_errors.SectionMissingErr {
			return nil, NewConfError(ErrObjectDoesNotExist, fmt.Sprintf("Frontend %s does not exist", frontend))
		}
		return nil, err
	}

	v, err := c.GetVersion(transactionID)
	if err != nil {
		return nil, err
	}

	if c.Cache.Enabled() {
		c.Cache.Binds.SetAll(frontend, transactionID, binds)
	}
	return &models.GetBindsOKBody{Version: v, Data: binds}, nil
}

// GetBind returns a struct with configuration version and a requested bind
// in the specified frontend. Returns error on fail or if bind does not exist.
func (c *Client) GetBind(name string, frontend string, transactionID string) (*models.GetBindOKBody, error) {
	if c.Cache.Enabled() {
		bind, found := c.Cache.Binds.GetOne(name, frontend, transactionID)
		if found {
			return &models.GetBindOKBody{Version: c.Cache.Version.Get(transactionID), Data: bind}, nil
		}
	}

	if err := c.ConfigParser.LoadData(c.getTransactionFile(transactionID)); err != nil {
		return nil, err
	}

	bind, _ := c.getBindByName(name, frontend)
	if bind == nil {
		return nil, NewConfError(ErrObjectDoesNotExist, fmt.Sprintf("Bind %s does not exist in frontend %s", name, frontend))
	}

	v, err := c.GetVersion(transactionID)
	if err != nil {
		return nil, err
	}

	if c.Cache.Enabled() {
		c.Cache.Binds.Set(name, frontend, transactionID, bind)
	}
	return &models.GetBindOKBody{Version: v, Data: bind}, nil
}

// DeleteBind deletes a bind in configuration. One of version or transactionID is
// mandatory. Returns error on fail, nil on success.
func (c *Client) DeleteBind(name string, frontend string, transactionID string, version int64) error {
	t, err := c.loadDataForChange(transactionID, version)
	if err != nil {
		return err
	}

	bind, i := c.getBindByName(name, frontend)
	if bind == nil {
		return NewConfError(ErrObjectDoesNotExist, fmt.Sprintf("Bind %s does not exist in frontend %s", name, frontend))
	}

	if err := c.ConfigParser.Delete(parser.Frontends, frontend, "bind", i); err != nil {
		if err == parser_errors.SectionMissingErr {
			return NewConfError(ErrObjectDoesNotExist, fmt.Sprintf("Frontend %s does not exist", frontend))
		}
		if err == parser_errors.FetchError {
			return NewConfError(ErrObjectDoesNotExist, fmt.Sprintf("Bind %s does not exist in frontend %s", name, frontend))
		}
		return err
	}

	if err := c.saveData(t, transactionID); err != nil {
		return err
	}
	if c.Cache.Enabled() {
		c.Cache.Binds.Delete(name, frontend, transactionID)
	}
	return nil
}

// CreateBind creates a bind in configuration. One of version or transactionID is
// mandatory. Returns error on fail, nil on success.
func (c *Client) CreateBind(frontend string, data *models.Bind, transactionID string, version int64) error {
	if c.UseValidation {
		validationErr := data.Validate(strfmt.Default)
		if validationErr != nil {
			return NewConfError(ErrValidationError, validationErr.Error())
		}
	}
	t, err := c.loadDataForChange(transactionID, version)
	if err != nil {
		return err
	}

	bind, _ := c.getBindByName(data.Name, frontend)
	if bind != nil {
		return c.errAndDeleteTransaction(NewConfError(ErrObjectAlreadyExists, fmt.Sprintf("Bind %s already exists in frontend %s", data.Name, frontend)),
			t, transactionID == "")
	}

	if err := c.ConfigParser.Insert(parser.Frontends, frontend, "bind", serializeBind(*data), -1); err != nil {
		if err == parser_errors.SectionMissingErr {
			return NewConfError(ErrObjectDoesNotExist, fmt.Sprintf("Frontend %s does not exist", frontend))
		}
		return c.errAndDeleteTransaction(err, t, transactionID == "")
	}

	if err := c.saveData(t, transactionID); err != nil {
		return err
	}

	if c.Cache.Enabled() {
		c.Cache.Binds.Set(data.Name, frontend, transactionID, data)
	}
	return nil
}

// EditBind edits a bind in configuration. One of version or transactionID is
// mandatory. Returns error on fail, nil on success.
func (c *Client) EditBind(name string, frontend string, data *models.Bind, transactionID string, version int64) error {
	if c.UseValidation {
		validationErr := data.Validate(strfmt.Default)
		if validationErr != nil {
			return NewConfError(ErrValidationError, validationErr.Error())
		}
	}
	t, err := c.loadDataForChange(transactionID, version)
	if err != nil {
		return err
	}

	bind, i := c.getBindByName(name, frontend)
	if bind == nil {
		return NewConfError(ErrObjectDoesNotExist, fmt.Sprintf("Bind %v does not exist in frontend %s", name, frontend))
	}

	if err := c.ConfigParser.Set(parser.Frontends, frontend, "bind", serializeBind(*data), i); err != nil {
		return c.errAndDeleteTransaction(err, t, transactionID == "")
	}

	if err := c.saveData(t, transactionID); err != nil {
		return err
	}

	if c.Cache.Enabled() {
		c.Cache.Binds.Set(name, frontend, transactionID, data)
	}
	return nil
}

func (c *Client) parseBinds(frontend string) (models.Binds, error) {
	binds := models.Binds{}

	data, err := c.ConfigParser.Get(parser.Frontends, frontend, "bind", false)
	if err != nil {
		if err == parser_errors.FetchError {
			return binds, nil
		}
		return nil, err
	}

	ondiskBinds := data.([]types.Bind)
	for _, ondiskBind := range ondiskBinds {
		b := parseBind(ondiskBind)
		if b != nil {
			binds = append(binds, b)
		}
	}
	return binds, nil
}

func parseBind(ondiskBind types.Bind) *models.Bind {
	b := &models.Bind{
		Name: ondiskBind.Path,
	}
	if strings.HasPrefix(ondiskBind.Path, "/") {
		b.Address = ondiskBind.Path
	} else {
		addSlice := strings.Split(ondiskBind.Path, ":")
		if len(addSlice) == 0 {
			return nil
		} else if len(addSlice) > 1 {
			b.Address = addSlice[0]
			if addSlice[1] != "" {
				p, err := strconv.ParseInt(addSlice[1], 10, 64)
				if err == nil {
					b.Port = &p
				}
			}
		} else if len(addSlice) > 0 {
			b.Address = addSlice[0]
		}
	}
	for _, p := range ondiskBind.Params {
		switch v := p.(type) {
		case *params.BindOptionWord:
			switch v.Name {
			case "ssl":
				b.Ssl = true
			case "transparent":
				b.Transparent = true
			}
		case *params.BindOptionValue:
			switch v.Name {
			case "name":
				b.Name = v.Value
			case "process":
				b.Process = v.Value
			case "tcp-ut":
				t, err := strconv.ParseInt(v.Value, 10, 64)
				if err == nil && t != 0 {
					b.TCPUserTimeout = &t
				}
			case "crt":
				b.SslCertificate = v.Value
			case "ca-file":
				b.SslCafile = v.Value
			}
		}
	}
	return b
}

func serializeBind(b models.Bind) types.Bind {
	bind := types.Bind{
		Params: []params.BindOption{},
	}
	if b.Port != nil {
		bind.Path = b.Address + ":" + strconv.FormatInt(*b.Port, 10)
	} else {
		bind.Path = b.Address
	}
	if b.Name != "" {
		bind.Params = append(bind.Params, &params.BindOptionValue{Name: "name", Value: b.Name})
	} else {
		bind.Params = append(bind.Params, &params.BindOptionValue{Name: "name", Value: bind.Path})
	}
	if b.Process != "" {
		bind.Params = append(bind.Params, &params.BindOptionValue{Name: "process", Value: b.Process})
	}
	if b.SslCertificate != "" {
		bind.Params = append(bind.Params, &params.BindOptionValue{Name: "crt", Value: b.SslCertificate})
	}
	if b.SslCafile != "" {
		bind.Params = append(bind.Params, &params.BindOptionValue{Name: "ca-file", Value: b.SslCafile})
	}
	if b.TCPUserTimeout != nil {
		bind.Params = append(bind.Params, &params.BindOptionValue{Name: "tcp-ut", Value: strconv.FormatInt(*b.TCPUserTimeout, 10)})
	}
	if b.Ssl {
		bind.Params = append(bind.Params, &params.BindOptionWord{Name: "ssl"})
	}
	if b.Transparent {
		bind.Params = append(bind.Params, &params.BindOptionWord{Name: "transparent"})
	}

	return bind
}

func (c *Client) getBindByName(name string, frontend string) (*models.Bind, int) {
	binds, err := c.parseBinds(frontend)
	if err != nil {
		return nil, 0
	}

	for i, b := range binds {
		if b.Name == name {
			return b, i
		}
	}
	return nil, 0
}